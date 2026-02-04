# Multica Desktop App 设计文档

## 产品定位

Multica Desktop 是一个统一的桌面应用，具有双重身份：

1. **Host 模式**: 本机运行 Hub + Agent，可供其他设备连接
2. **Client 模式**: 连接到其他 Hub 的 Agent 进行对话

用户安装同一个 App，既可以作为 Agent 的宿主（让其他设备扫码连接），也可以扫码连接到别人的 Agent。

### 架构图

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                           Multica Desktop App                                │
│                                                                             │
│  ┌─────────────────────────────────────────────────────────────────────┐   │
│  │                         React UI (Renderer)                          │   │
│  │  ┌─────────┐  ┌─────────┐  ┌─────────┐  ┌─────────┐  ┌─────────┐  │   │
│  │  │  Home   │  │  Chat   │  │  Tools  │  │ Skills  │  │Settings │  │   │
│  │  └─────────┘  └─────────┘  └─────────┘  └─────────┘  └─────────┘  │   │
│  └─────────────────────────────────────────────────────────────────────┘   │
│                                    │                                         │
│                    ┌───────────────┴───────────────┐                        │
│                    │                               │                        │
│              直接调用 (本地)              WebSocket (远程)                   │
│                    │                               │                        │
│                    ▼                               ▼                        │
│  ┌─────────────────────────────┐     ┌─────────────────────────────┐       │
│  │    Local Hub + Agent        │     │   Remote Hub (via Gateway)   │       │
│  │    (进程内)                  │     │   (另一台设备)               │       │
│  └─────────────────────────────┘     └─────────────────────────────┘       │
└─────────────────────────────────────────────────────────────────────────────┘
                                              │
                                              │ WebSocket
                                              ▼
                                    ┌─────────────────────┐
                                    │      Gateway        │
                                    │   (公网 WebSocket)  │
                                    └─────────────────────┘
```

**关键点**:

-  **统一应用**: 不区分 Admin App 和 Client App，一个 App 两种用法
-  **Chat 双模式**: Chat 页面可以选择与本地 Agent 对话，或连接远程 Agent 对话
-  **本地 Agent**: Hub + Agent 跑在 Electron 主进程内，UI 通过 IPC 调用访问
-  **远程连接**: 通过 Gateway WebSocket 连接到其他设备的 Hub

**约束**: 第一阶段 1 Client - 1 Hub - 1 Agent Session

---

## 技术实现设计

### 技术栈

| 层级   | 技术                     | 说明           |
| ------ | ------------------------ | -------------- |
| 框架   | Electron 30              | 桌面应用       |
| 前端   | React 19 + Vite          | 渲染进程       |
| 路由   | react-router-dom v7      | HashRouter     |
| 状态   | @multica/store (Zustand) | 复用现有 store |
| UI     | @multica/ui (Shadcn)     | 复用现有组件   |
| 二维码 | qrcode.react             | 生成二维码     |
| 通信   | @multica/sdk             | Gateway 连接   |

### 文件结构规划

```
apps/desktop/
├── electron/
│   ├── main.ts           # 主进程 (Hub + Agent)
│   └── preload.ts        # 预加载脚本 (如需 IPC)
├── src/
│   ├── main.tsx          # React 入口
│   ├── App.tsx           # 路由配置
│   ├── pages/
│   │   ├── home.tsx      # Home 入口页 (三个选项)
│   │   ├── chat.tsx      # Chat 页面 (Local/Remote 双模式)
│   │   ├── tools.tsx     # Tools 管理页
│   │   ├── skills.tsx    # Skills 管理页
│   │   └── layout.tsx    # 全局布局 (Header + Tabs)
│   ├── components/
│   │   ├── qr-code.tsx           # 二维码组件
│   │   ├── qr-scanner.tsx        # 扫码组件
│   │   ├── connection-status.tsx # 连接状态
│   │   ├── tool-list.tsx         # Tools 列表
│   │   └── skill-list.tsx        # Skills 列表
│   └── hooks/
│       ├── use-local-agent.ts    # 本地 Agent 管理
│       ├── use-remote-agent.ts   # 远程 Agent 连接
│       └── use-connection.ts     # 连接状态管理
└── package.json
```

### 核心实现点

#### 1. 二维码生成与连接

二维码内容格式:

```json
{
   "type": "multica-connect",
   "gateway": "wss://gateway.multica.ai",
   "hubId": "019c1d32-xxxx",
   "agentId": "019c1d32-yyyy",
   "token": "random-uuid-token",
   "expires": 1234567890
}
```

连接流程:

```
1. Admin 启动 → Hub 连接公网 Gateway → 注册为 deviceType: "hub"
2. Admin 创建 Agent → 生成 token → 编码到二维码 (含 hubId + agentId + token)
3. Client 扫码 → 解析二维码 → 连接同一 Gateway
4. Client 发送 "connect-request" 到 hubId (带 token)
5. Admin 验证 token 有效且未过期 → 建立配对关系
6. Client 后续消息发到 hubId，payload 带 agentId
7. Hub 路由消息到对应 Agent
```

#### 2. Tools 管理

**现有 CLI 命令** (已实现):

```bash
multica tools list                    # 列出所有 tools
multica tools list --profile coding   # 按 profile 过滤
multica tools groups                  # 显示 tool groups
multica tools profiles                # 显示预设 profiles
```

**Admin App 实现方式** - 通过 IPC 调用 Main Process:

```typescript
// Renderer 进程 (React Hook)
const tools = await window.electronAPI.tools.list();
const groups = await window.electronAPI.tools.getGroups();
const profiles = await window.electronAPI.tools.getProfiles();
await window.electronAPI.tools.setStatus('exec', false);

// Main 进程 (IPC Handler)
ipcMain.handle('tools:list', async () => {
   const allTools = createAllTools(process.cwd());
   return allTools.map((t) => ({
      name: t.name,
      group: TOOL_GROUPS[t.name],
      enabled: true,
   }));
});
```

**注意**: Renderer 进程运行在沙盒中，不能直接访问 Node.js API，必须通过 IPC 调用 Main Process。

#### 3. Skills 管理

**现有 CLI 命令** (已实现):

```bash
multica skills list                   # 列出所有 skills
multica skills status                 # 显示状态摘要
multica skills status <id>            # 单个 skill 详情
multica skills add owner/repo         # 从 GitHub 添加
multica skills remove <name>          # 删除 skill
multica skills install <id>           # 安装依赖
```

**Admin App 实现方式** - 通过 IPC 调用 Main Process:

```typescript
// Renderer 进程 (React Hook)
const skills = await window.electronAPI.skills.list();
await window.electronAPI.skills.add('anthropics/skills');
await window.electronAPI.skills.remove('pdf');
await window.electronAPI.skills.setEnabled('commit', false);

// Main 进程 (IPC Handler)
ipcMain.handle('skills:list', async () => {
   return await listAllSkillsWithStatus();
});
ipcMain.handle('skills:add', async (_, source: string) => {
   await addSkill({ source, force: false });
});
```

---

## 三、实现优先级

### Phase 1: 基础框架 (MVP)

1. **Layout 组件** - Header + Tabs 导航
2. **Home 页面** - 二维码显示 + 连接状态
3. **Gateway 连接** - 复用 @multica/store

### Phase 2: 管理功能

4. **Tools 页面** - 列表展示 + 开关切换
5. **Skills 页面** - 列表展示 + 基础操作
6. **Settings** - Gateway URL + Theme

### Phase 3: 完善体验

7. **Agent 页面** - 状态监控 + Provider 切换
8. **二维码刷新机制**
9. **错误处理 + Toast 提示**

---

## 四、Hub 集成技术方案

### 架构概述

Desktop App 采用 **Electron IPC + Hub 实例** 架构：

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                           Electron Desktop App                               │
│                                                                             │
│  ┌────────────────────────────────────────────────────────────────────────┐ │
│  │                     Renderer Process (React UI)                         │ │
│  │                                                                        │ │
│  │   home.tsx  →  useHub()  →  window.electronAPI.hub.getStatus()        │ │
│  │   tools.tsx →  useTools() → window.electronAPI.tools.list()           │ │
│  │   skills.tsx→  useSkills()→ window.electronAPI.skills.list()          │ │
│  │                                                                        │ │
│  └──────────────────────────────┬─────────────────────────────────────────┘ │
│                                 │ IPC (contextBridge)                       │
│                                 ▼                                           │
│  ┌────────────────────────────────────────────────────────────────────────┐ │
│  │                      Main Process (Node.js)                             │ │
│  │                                                                        │ │
│  │   ┌──────────────────────────────────────────────────────────────┐    │ │
│  │   │                    Hub Instance                               │    │ │
│  │   │  - hubId: UUIDv7                                             │    │ │
│  │   │  - agents: Map<agentId, AsyncAgent>                          │    │ │
│  │   │  - status: 'starting' | 'ready' | 'error'                    │    │ │
│  │   │  - GatewayClient: 连接公网 Gateway (可选)                     │    │ │
│  │   └──────────────────────────────────────────────────────────────┘    │ │
│  │                                │                                       │ │
│  │   ┌────────────────────────────▼────────────────────────────────┐     │ │
│  │   │                  AsyncAgent Instance                         │     │ │
│  │   │  - agentId: UUIDv7                                          │     │ │
│  │   │  - runner: AgentRunner (LLM interaction)                    │     │ │
│  │   │  - tools: Tool[] (可动态更新)                                │     │ │
│  │   │  - skills: SkillInfo[]                                      │     │ │
│  │   └─────────────────────────────────────────────────────────────┘     │ │
│  │                                                                        │ │
│  └────────────────────────────────────────────────────────────────────────┘ │
└─────────────────────────────────────────────────────────────────────────────┘
                                    │
                                    │ WebSocket (可选，用于 Client 远程连接)
                                    ▼
                          ┌─────────────────────┐
                          │  Public Gateway     │
                          │  (wss://xxx)        │
                          └─────────────────────┘
```

### IPC 通信机制

**工作原理**:

1. **Main Process**: 在 Electron 主进程中创建 Hub 和 Agent 实例
2. **Preload Script**: 通过 `contextBridge.exposeInMainWorld` 暴露安全 API
3. **Renderer Process**: React UI 通过 `window.electronAPI` 调用主进程功能

**与 CLI 命令的关系**:

| CLI 命令                   | IPC Handler       | 底层调用                                     |
| -------------------------- | ----------------- | -------------------------------------------- |
| `multica tools list`       | `tools:list`      | `createAllTools()` + `getToolStatus()`       |
| `multica tools enable xxx` | `tools:setStatus` | `setToolStatus()`                            |
| `multica skills list`      | `skills:list`     | `loadSkills()` + `listAllSkillsWithStatus()` |
| `multica skills add xxx`   | `skills:add`      | `addSkill()`                                 |

**本质上 CLI 和 Admin App 调用的是同一套底层模块**，区别仅在于：

-  CLI: 通过命令行参数解析后直接调用
-  Admin App: 通过 IPC 转发调用

### 核心文件

```
apps/desktop/
├── electron/
│   ├── main.ts                 # 主进程入口，创建窗口 + 注册 IPC
│   ├── preload.ts              # 暴露 electronAPI
│   └── ipc/
│       ├── index.ts            # 统一注册所有 IPC handlers
│       ├── hub.ts              # Hub 管理 (创建/状态/连接 Gateway)
│       ├── agent.ts            # Agent 管理 (Tools 读写)
│       └── skills.ts           # Skills 管理
├── src/
│   └── hooks/
│       ├── use-hub.ts          # 获取 Hub 状态
│       ├── use-tools.ts        # Tools CRUD
│       └── use-skills.ts       # Skills CRUD
```

### IPC 接口定义

```typescript
// electron/preload.ts 暴露的 API
interface ElectronAPI {
   hub: {
      getStatus: () => Promise<HubStatus>;
      getAgentInfo: () => Promise<AgentInfo | null>;
   };
   tools: {
      list: () => Promise<ToolStatus[]>;
      setStatus: (toolName: string, enabled: boolean) => Promise<void>;
      getGroups: () => Promise<Record<string, string[]>>;
      getProfiles: () => Promise<string[]>;
   };
   skills: {
      list: () => Promise<SkillInfo[]>;
      add: (source: string) => Promise<void>;
      remove: (name: string) => Promise<void>;
      setEnabled: (name: string, enabled: boolean) => Promise<void>;
   };
}

// 类型定义
interface HubStatus {
   hubId: string;
   status: 'starting' | 'ready' | 'error';
   agentCount: number;
   gatewayConnected: boolean;
   gatewayUrl?: string;
}

interface AgentInfo {
   agentId: string;
   provider: string;
   model: string;
   status: 'idle' | 'running';
}

interface ToolStatus {
   name: string;
   group: string;
   enabled: boolean;
   needsConfig?: boolean;
}

interface SkillInfo {
   name: string;
   command: string;
   source: 'bundled' | 'global' | 'profile';
   status: 'ready' | 'missing-deps' | 'disabled';
   description?: string;
}
```

### Hub 生命周期

```typescript
// electron/ipc/hub.ts 简化逻辑

let hub: Hub | null = null;

export function registerHubHandlers(ipcMain: IpcMain) {
   // App 启动时自动创建 Hub
   ipcMain.handle('hub:getStatus', async () => {
      if (!hub) {
         hub = new Hub();
         await hub.start();
         // 创建默认 Agent
         const agent = await hub.createAgent({
            provider: credentialManager.getLlmProvider(),
            model: credentialManager.getLlmProviderConfig()?.model,
         });
      }
      return {
         hubId: hub.id,
         status: hub.status,
         agentCount: hub.agents.size,
         gatewayConnected: hub.gateway?.connected ?? false,
      };
   });
}
```

### Tools 实时更新机制

当用户在 UI 中切换 Tool 开关时：

```
1. UI: Switch onChange → useTools.setToolStatus('exec', false)
2. Hook: await window.electronAPI.tools.setStatus('exec', false)
3. IPC: ipcMain.handle('tools:setStatus') → agent.updateTools(...)
4. Agent: 重新过滤 tools 列表，下次 LLM 调用使用新配置
```

**注意**: Tools 状态目前保存在内存中，重启后重置。后续可持久化到 `~/.super-multica/tool-config.json`。

---

## 六、关于 RPC 与 IPC 的区别

**问**: Admin UI 和 Hub/Agent 之间是通过什么方式通信？

**答**: 通过 **Electron IPC (进程间通信)**，不是网络 RPC。

| 通信类型 | 场景                            | 协议                |
| -------- | ------------------------------- | ------------------- |
| IPC      | Admin UI ↔ Hub (同一设备)       | Electron IPC (内存) |
| RPC      | Client ↔ Gateway ↔ Hub (跨设备) | WebSocket           |

**为什么选择 IPC 而不是直接 import?**

1. **安全隔离**: Renderer 进程不应直接访问 Node.js API 和文件系统
2. **进程隔离**: Electron 推荐 Renderer 运行在沙盒中
3. **一致性**: 与 CLI 调用相同的底层模块，便于维护
4. **扩展性**: 后续可以轻松添加 RPC 支持，供远程管理

```
┌─────────────────────────────────────────────────────────────────┐
│                       Electron App                               │
│                                                                 │
│  ┌──────────────────────┐      ┌─────────────────────────────┐ │
│  │  Renderer Process    │      │     Main Process            │ │
│  │  (React UI, 沙盒)    │      │     (Node.js, 完整权限)      │ │
│  │                      │ IPC  │                             │ │
│  │  useTools() ──────────────► │  ipcMain.handle('tools:*')  │ │
│  │  useSkills() ─────────────► │  ipcMain.handle('skills:*') │ │
│  │  useHub() ────────────────► │  Hub + Agent 实例           │ │
│  └──────────────────────┘      └─────────────────────────────┘ │
└─────────────────────────────────────────────────────────────────┘
```

**IPC 调用示例**:

```typescript
// Renderer (React 组件)
const tools = await window.electronAPI.tools.list();

// Main Process (IPC Handler)
ipcMain.handle('tools:list', async () => {
   const allTools = createAllTools(process.cwd());
   return allTools.map((t) => ({
      name: t.name,
      group: TOOL_GROUPS[t.name] || 'other',
      enabled: getToolStatus(t.name),
   }));
});
```

---

## 七、依赖安装

```bash
# 二维码生成
pnpm --filter @multica/desktop add qrcode.react

# 类型定义 (如需要)
pnpm --filter @multica/desktop add -D @types/qrcode.react
```

---

## 八、数据流架构

Chat 页面支持两种模式，底层使用相同的 UI 组件和 Store：

### Local Mode (IPC 直连)

本地 Agent 对话，不需要 Gateway，直接通过 Electron IPC 通信：

```
ChatInput → useLocalChat.sendMessage()
  → IPC: localChat:send → agent.write()
  → agent.subscribe() → IPC: localChat:event
  → useLocalChat.onEvent() → useMessagesStore.startStream/appendStream/endStream
  → MessageList 显示
```

### Remote Mode (Gateway)

远程 Agent 对话，通过 WebSocket 连接 Gateway：

```
ChatInput → useMessagesStore.sendMessage()
  → ConnectionStore.send() → WebSocket → Gateway → Hub → agent.write()
  → Hub.consumeAgent() → WebSocket: stream event
  → ConnectionStore.onMessage() → useMessagesStore.startStream/appendStream/endStream
  → MessageList 显示
```

### 复用层级

| 层级       | 组件/模块                                | 复用情况 |
| ---------- | ---------------------------------------- | -------- |
| UI 层      | `MessageList`, `ChatInput`               | ✅ 完全复用 |
| Store 层   | `useMessagesStore`                       | ✅ 完全复用 |
| Agent 层   | `AsyncAgent.write()`, `subscribe()`      | ✅ 完全复用 |
| 传输层     | IPC vs WebSocket                         | ❌ 各自实现 |

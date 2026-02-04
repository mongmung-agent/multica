/**
 * Chat Page - supports both Local (IPC) and Remote (Gateway) modes
 *
 * Both modes use the same useMessagesStore and Chat UI components.
 * The difference is only in the transport layer:
 * - Local: Direct IPC to agent in the same Electron process
 * - Remote: WebSocket via Gateway to external Hub
 */
import { useState, useEffect, useCallback, useRef } from 'react'
import { Button } from '@multica/ui/components/ui/button'
import { ChatInput } from '@multica/ui/components/chat-input'
import { MessageList } from '@multica/ui/components/message-list'
import { ConnectPrompt } from '@multica/ui/components/connect-prompt'
import { useMessagesStore, useConnectionStore, useAutoConnect } from '@multica/store'
import { useScrollFade } from '@multica/ui/hooks/use-scroll-fade'
import { useAutoScroll } from '@multica/ui/hooks/use-auto-scroll'
import { useLocalChat } from '../hooks/use-local-chat'

type ChatMode = 'select' | 'local' | 'remote'

export default function ChatPage() {
  const [mode, setMode] = useState<ChatMode>('select')
  const [defaultAgentId, setDefaultAgentId] = useState<string | null>(null)

  // Get default agent ID on mount
  useEffect(() => {
    const loadAgentId = async () => {
      const status = await window.electronAPI.hub.getStatus()
      if (status.defaultAgent?.agentId) {
        setDefaultAgentId(status.defaultAgent.agentId)
      }
    }
    loadAgentId()
  }, [])

  // Clear messages when switching modes
  const handleModeChange = (newMode: ChatMode) => {
    useMessagesStore.getState().clearMessages()
    setMode(newMode)
  }

  // Mode selection screen
  if (mode === 'select') {
    return (
      <div className="h-full flex flex-col items-center justify-center gap-6 p-4">
        <div className="text-center space-y-2">
          <h2 className="text-lg font-semibold">Start a Conversation</h2>
          <p className="text-sm text-muted-foreground">
            Choose how you want to connect
          </p>
        </div>

        <div className="flex flex-col gap-3 w-full max-w-xs">
          <Button
            size="lg"
            onClick={() => handleModeChange('local')}
            disabled={!defaultAgentId}
            className="w-full"
          >
            Local Agent
            <span className="text-xs ml-2 opacity-70">(Direct IPC)</span>
          </Button>

          <Button
            size="lg"
            variant="outline"
            onClick={() => handleModeChange('remote')}
            className="w-full"
          >
            Remote Agent
            <span className="text-xs ml-2 opacity-70">(Via Gateway)</span>
          </Button>
        </div>

        {!defaultAgentId && (
          <p className="text-xs text-muted-foreground">
            Waiting for local agent to initialize...
          </p>
        )}
      </div>
    )
  }

  // Local chat mode - uses useLocalChat hook that bridges to useMessagesStore
  if (mode === 'local' && defaultAgentId) {
    return <LocalChatView agentId={defaultAgentId} onBack={() => handleModeChange('select')} />
  }

  // Remote chat mode - uses Gateway connection
  return <RemoteChatView onBack={() => handleModeChange('select')} />
}

/**
 * Local Chat View - Direct IPC communication with agent
 * Uses useLocalChat hook which bridges IPC events to useMessagesStore
 */
function LocalChatView({ agentId, onBack }: { agentId: string; onBack: () => void }) {
  const { isConnected, isLoading, sendMessage, disconnect } = useLocalChat({ agentId })

  // Use same stores as Gateway mode
  const messages = useMessagesStore((s) => s.messages)
  const streamingIds = useMessagesStore((s) => s.streamingIds)

  const mainRef = useRef<HTMLElement>(null)
  const fadeStyle = useScrollFade(mainRef)
  useAutoScroll(mainRef)

  const handleDisconnect = useCallback(() => {
    disconnect()
    onBack()
  }, [disconnect, onBack])

  return (
    <div className="h-full flex flex-col overflow-hidden w-full">
      {/* Header */}
      <div className="flex items-center justify-between px-4 py-2 border-b">
        <div className="flex items-center gap-2">
          <Button variant="ghost" size="sm" onClick={onBack}>
            ← Back
          </Button>
          <span className="text-sm font-medium">Local Agent</span>
          <span
            className={`w-2 h-2 rounded-full ${isConnected ? 'bg-green-500' : 'bg-gray-400'}`}
          />
        </div>
        <Button
          variant="ghost"
          size="sm"
          onClick={handleDisconnect}
          className="text-xs text-muted-foreground"
        >
          Disconnect
        </Button>
      </div>

      {/* Messages - same component as Gateway mode */}
      <main ref={mainRef} className="flex-1 overflow-y-auto min-h-0" style={fadeStyle}>
        {messages.length === 0 ? (
          <div className="flex items-center justify-center h-full text-muted-foreground text-sm">
            Send a message to start the conversation
          </div>
        ) : (
          <MessageList messages={messages} streamingIds={streamingIds} />
        )}
      </main>

      {/* Input - same component as Gateway mode */}
      <footer className="w-full p-2 pt-1 max-w-4xl mx-auto">
        <ChatInput
          onSubmit={sendMessage}
          disabled={!isConnected || isLoading}
          placeholder={!isConnected ? 'Connecting...' : 'Type a message...'}
        />
      </footer>
    </div>
  )
}

/**
 * Remote Chat View - Gateway connection to external Hub
 * Same as the original Chat component
 */
function RemoteChatView({ onBack }: { onBack: () => void }) {
  const { loading } = useAutoConnect()

  const agentId = useConnectionStore((s) => s.agentId)
  const gwState = useConnectionStore((s) => s.connectionState)
  const hubId = useConnectionStore((s) => s.hubId)

  const messages = useMessagesStore((s) => s.messages)
  const streamingIds = useMessagesStore((s) => s.streamingIds)

  const isConnected = gwState === 'registered' && !!hubId && !!agentId

  const handleSend = useCallback((text: string) => {
    const { hubId, agentId, send, connectionState } = useConnectionStore.getState()
    if (connectionState !== 'registered' || !hubId || !agentId) return
    useMessagesStore.getState().sendMessage(text, { hubId, agentId, send })
  }, [])

  const handleDisconnect = useCallback(() => {
    useConnectionStore.getState().disconnect()
    onBack()
  }, [onBack])

  const mainRef = useRef<HTMLElement>(null)
  const fadeStyle = useScrollFade(mainRef)
  useAutoScroll(mainRef)

  return (
    <div className="h-full flex flex-col overflow-hidden w-full">
      {/* Header */}
      <div className="flex items-center justify-between px-4 py-2 border-b">
        <div className="flex items-center gap-2">
          <Button variant="ghost" size="sm" onClick={onBack}>
            ← Back
          </Button>
          <span className="text-sm font-medium">Remote Agent</span>
        </div>
        {isConnected && (
          <Button
            variant="ghost"
            size="sm"
            onClick={handleDisconnect}
            className="text-xs text-muted-foreground"
          >
            Disconnect
          </Button>
        )}
      </div>

      {/* Messages */}
      <main ref={mainRef} className="flex-1 overflow-y-auto min-h-0" style={fadeStyle}>
        {loading ? (
          <div className="flex items-center justify-center h-full text-muted-foreground text-sm">
            Loading...
          </div>
        ) : !isConnected ? (
          <ConnectPrompt />
        ) : messages.length === 0 ? (
          <div className="flex items-center justify-center h-full text-muted-foreground text-sm">
            Send a message to start the conversation
          </div>
        ) : (
          <MessageList messages={messages} streamingIds={streamingIds} />
        )}
      </main>

      {/* Input */}
      <footer className="w-full p-2 pt-1 max-w-4xl mx-auto">
        <ChatInput
          onSubmit={handleSend}
          disabled={!isConnected}
          placeholder={!isConnected ? 'Connect first...' : 'Type a message...'}
        />
      </footer>
    </div>
  )
}

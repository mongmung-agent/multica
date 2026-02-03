import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from '@multica/ui/components/ui/card'
import { useTools } from '../hooks/use-tools'
import { ToolList } from '../components/tool-list'

export default function ToolsPage() {
  const {
    tools,
    groups,
    loading,
    error,
    toggleTool,
    refresh,
  } = useTools()

  return (
    <div className="max-w-4xl mx-auto">
      <Card>
        <CardHeader>
          <CardTitle>Tools</CardTitle>
          <CardDescription>
            Configure which tools are available to the Agent. Toggle individual tools on/off.
            Changes apply immediately to the running Agent.
          </CardDescription>
        </CardHeader>
        <CardContent>
          <ToolList
            tools={tools}
            groups={groups}
            loading={loading}
            error={error}
            onToggleTool={toggleTool}
            onRefresh={refresh}
          />
        </CardContent>
      </Card>
    </div>
  )
}

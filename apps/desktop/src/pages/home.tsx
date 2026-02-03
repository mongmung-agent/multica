import { useNavigate } from 'react-router-dom'
import { Button } from '@multica/ui/components/ui/button'
import { HugeiconsIcon } from '@hugeicons/react'
import {
  Comment01Icon,
  LinkSquare01Icon,
  Loading03Icon,
  AlertCircleIcon,
} from '@hugeicons/core-free-icons'
import { ConnectionQRCode } from '../components/qr-code'
import { useHub } from '../hooks/use-hub'

export default function HomePage() {
  const navigate = useNavigate()
  const { hubInfo, agents, loading, error } = useHub()

  // Get the first agent (or create one if none exists)
  const primaryAgent = agents[0]

  // Connection state indicator
  // Note: 'registered' means fully connected and registered with Gateway
  const connectionState = hubInfo?.connectionState ?? 'disconnected'
  const isConnected = connectionState === 'connected' || connectionState === 'registered'

  // Loading state
  if (loading) {
    return (
      <div className="h-full flex items-center justify-center">
        <div className="flex items-center gap-3 text-muted-foreground">
          <HugeiconsIcon icon={Loading03Icon} className="size-5 animate-spin" />
          <span>Connecting to Hub...</span>
        </div>
      </div>
    )
  }

  // Error state
  if (error) {
    return (
      <div className="h-full flex items-center justify-center">
        <div className="flex flex-col items-center gap-3 text-destructive">
          <HugeiconsIcon icon={AlertCircleIcon} className="size-8" />
          <span className="font-medium">Connection Error</span>
          <span className="text-sm text-muted-foreground">{error}</span>
        </div>
      </div>
    )
  }

  return (
    <div className="h-full flex flex-col">
      {/* Main content - QR + Status */}
      <div className="flex-1 flex gap-8 p-2">
        {/* Left: QR Code */}
        <div className="flex-1 flex flex-col items-center justify-center">
          <ConnectionQRCode
            gateway={hubInfo?.url ?? 'http://localhost:3000'}
            hubId={hubInfo?.hubId ?? 'unknown'}
            agentId={primaryAgent?.id}
            expirySeconds={300}
            size={180}
          />
        </div>

        {/* Right: Hub Status */}
        <div className="flex-1 flex flex-col justify-center">
          <div className="space-y-6">
            {/* Hub Header */}
            <div>
              <div className="flex items-center gap-2 mb-1">
                <span className="relative flex size-2.5">
                  {isConnected ? (
                    <>
                      <span className="animate-ping absolute inline-flex h-full w-full rounded-full bg-green-400 opacity-75" />
                      <span className="relative inline-flex rounded-full size-2.5 bg-green-500" />
                    </>
                  ) : connectionState === 'connecting' || connectionState === 'reconnecting' ? (
                    <>
                      <span className="animate-ping absolute inline-flex h-full w-full rounded-full bg-yellow-400 opacity-75" />
                      <span className="relative inline-flex rounded-full size-2.5 bg-yellow-500" />
                    </>
                  ) : (
                    <span className="relative inline-flex rounded-full size-2.5 bg-red-500" />
                  )}
                </span>
                <span className={`text-sm font-medium ${
                  isConnected
                    ? 'text-green-600 dark:text-green-400'
                    : connectionState === 'connecting' || connectionState === 'reconnecting'
                    ? 'text-yellow-600 dark:text-yellow-400'
                    : 'text-red-600 dark:text-red-400'
                }`}>
                  {isConnected
                    ? 'Hub Connected'
                    : connectionState === 'connecting'
                    ? 'Connecting...'
                    : connectionState === 'reconnecting'
                    ? 'Reconnecting...'
                    : 'Disconnected'}
                </span>
              </div>
              <h2 className="text-2xl font-semibold tracking-tight">
                Local Hub
              </h2>
              <p className="text-sm text-muted-foreground font-mono">
                {hubInfo?.hubId ?? 'Initializing...'}
              </p>
            </div>

            {/* Stats Grid */}
            <div className="grid grid-cols-2 gap-4">
              <div className="p-4 rounded-lg bg-muted/50 border border-border/50">
                <p className="text-xs text-muted-foreground uppercase tracking-wider mb-1">
                  Gateway
                </p>
                <p className="font-medium text-sm truncate" title={hubInfo?.url}>
                  {hubInfo?.url ?? '-'}
                </p>
              </div>
              <div className="p-4 rounded-lg bg-muted/50 border border-border/50">
                <p className="text-xs text-muted-foreground uppercase tracking-wider mb-1">
                  Connection
                </p>
                <p className="font-medium capitalize">{connectionState}</p>
              </div>
              <div className="p-4 rounded-lg bg-muted/50 border border-border/50">
                <p className="text-xs text-muted-foreground uppercase tracking-wider mb-1">
                  Active Agents
                </p>
                <p className="font-medium">{hubInfo?.agentCount ?? 0}</p>
              </div>
              <div className="p-4 rounded-lg bg-muted/50 border border-border/50">
                <p className="text-xs text-muted-foreground uppercase tracking-wider mb-1">
                  Primary Agent
                </p>
                <p className="font-medium text-sm font-mono truncate" title={primaryAgent?.id}>
                  {primaryAgent?.id ?? 'None'}
                </p>
              </div>
            </div>
          </div>
        </div>
      </div>

      {/* Bottom: Actions */}
      <div className="border-t p-4">
        <div className="flex items-center justify-between">
          {/* Primary Action: Chat */}
          <Button
            size="lg"
            className="gap-2 px-6"
            onClick={() => navigate('/chat')}
            disabled={!isConnected}
          >
            <HugeiconsIcon icon={Comment01Icon} className="size-5" />
            Open Chat
          </Button>

          {/* Secondary: Connect to Remote */}
          <Button
            variant="ghost"
            size="sm"
            className="text-muted-foreground gap-1.5"
            disabled
          >
            <HugeiconsIcon icon={LinkSquare01Icon} className="size-4" />
            Connect to Remote Agent
            <span className="text-xs opacity-60">(Coming soon)</span>
          </Button>
        </div>
      </div>
    </div>
  )
}

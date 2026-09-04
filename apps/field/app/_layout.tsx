import { useEffect, useState } from 'react'
import { Stack } from 'expo-router'
import { StatusBar } from 'expo-status-bar'

import { WalletProvider } from '../src/wallet/WalletProvider'
import * as api from '../src/wildtag/api'
import * as settings from '../src/wildtag/settings'
import type { WalletChain } from '../src/wildtag/types'
import { theme } from '../src/ui/theme'

export default function RootLayout() {
  const [deployment, setDeployment] = useState<{ chain: WalletChain; arcadeURL: string } | null>(
    null
  )

  // The server URL and the session token are read from disk before anything
  // renders. A screen that mounts, fires a request with no token, and gets a
  // 401 would sign the user out for no reason.
  //
  // The chain comes from disk too, for a sharper reason: the wallet is built
  // once at launch, and one built for the wrong network cannot verify a
  // payment made on the right one -- the merkle proof is checked against
  // headers from a chain the transaction was never on.
  useEffect(() => {
    void (async () => {
      await api.init()
      setDeployment({ chain: await settings.chain(), arcadeURL: await settings.arcadeURL() })
      // Then correct it in the background if the deployment disagrees. Asking
      // the server first would mean no wallet at all for a phone with no
      // signal, which is most of the time this app is open.
      void settings.learnDeployment().catch(() => {
        /* offline; the cached values stand */
      })
    })()
  }, [])

  if (!deployment) return null

  return (
    <WalletProvider chain={deployment.chain} arcadeURL={deployment.arcadeURL}>
      <StatusBar style="light" />
      <Stack
        screenOptions={{
          headerStyle: { backgroundColor: theme.panel },
          headerTintColor: theme.ink,
          contentStyle: { backgroundColor: theme.bg }
        }}
      >
        <Stack.Screen name="index" options={{ title: 'WildTag' }} />
        <Stack.Screen name="scan" options={{ title: 'Scan a tag' }} />
        <Stack.Screen name="report" options={{ title: 'Report a tag' }} />
        <Stack.Screen name="wallet" options={{ title: 'Wallet' }} />
        <Stack.Screen name="settings" options={{ title: 'Settings' }} />
        <Stack.Screen name="admin/login" options={{ title: 'Sign in' }} />
        <Stack.Screen name="admin/arm" options={{ title: 'Arm a tag' }} />
        <Stack.Screen name="admin/queue" options={{ title: 'Waiting to send' }} />
      </Stack>
    </WalletProvider>
  )
}

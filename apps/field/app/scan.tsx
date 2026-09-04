/**
 * The camera.
 *
 * A tag's QR carries `<origin>/t/<TAG-ID>#<secret>`, and the secret is what
 * unlocks the reward. It is in the URL fragment on purpose: a fragment never
 * leaves the client, so scanning does not put a bearer instrument into an
 * access log. This screen therefore parses the code on-device and passes the
 * secret to the next screen in memory, never through a request.
 */
import { useRef, useState } from 'react'
import { CameraView, useCameraPermissions } from 'expo-camera'
import { useRouter } from 'expo-router'
import { StyleSheet, View } from 'react-native'

import { Banner, Button, Card, H2, Note, P, Screen } from '../src/ui/atoms'
import { theme } from '../src/ui/theme'
import * as settings from '../src/wildtag/settings'
import { BadTagCode, parse } from '../src/wildtag/tagkey'

export default function Scan() {
  const router = useRouter()
  const [permission, requestPermission] = useCameraPermissions()
  const [error, setError] = useState<string | null>(null)
  // Scanning fires repeatedly while the code is in frame; without this the
  // router would be pushed a dozen times for one tag.
  const handled = useRef(false)
  // The last code that failed to parse, so a QR the camera keeps seeing is
  // reported once rather than on every frame.
  const rejected = useRef<string | null>(null)

  if (!permission) return null

  if (!permission.granted) {
    return (
      <Screen>
        <Card>
          <H2>Camera</H2>
          <P>Reading a tag means reading the QR code printed on it, so this app needs the camera.</P>
          <Button title="Allow camera" onPress={() => void requestPermission()} />
        </Card>
      </Screen>
    )
  }

  const onScanned = async ({ data }: { data: string }) => {
    if (handled.current) return

    let tag: ReturnType<typeof parse>
    try {
      tag = parse(data)
    } catch (err) {
      // Latch on the *code*, not on success. onBarcodeScanned fires for every
      // frame the code is in view -- tens of times a second -- so re-reporting
      // the same unreadable QR would call setState on every one of them and
      // render the screen into the ground. Remember what was rejected and stay
      // quiet until something different appears.
      if (rejected.current !== data) {
        rejected.current = data
        setError(err instanceof BadTagCode ? err.message : 'That code could not be read.')
      }
      return
    }
    handled.current = true
    try {
      // A scanned tag names the deployment that issued it, which is where the
      // report has to go. Awaited rather than fired off: the next screen asks
      // that server for the tag immediately, and starting it before the address
      // is stored is a race that happens to work today only because of the
      // order of two statements in api.setServer.
      //
      // Only adopted when nothing is configured yet, so a tag from another DNR
      // cannot silently repoint a signed-in tagger's console at a server they
      // did not choose.
      await settings.adopt(tag.origin)
      router.replace({
        pathname: '/report',
        params: {
          tagID: tag.tagID,
          display: tag.display,
          secret: tag.secret.join(','),
          origin: tag.origin
        }
      })
    } catch (err) {
      // Storing the address failed, which is not the tag's fault; let them try
      // again rather than stranding them on a camera that has stopped scanning.
      handled.current = false
      rejected.current = null
      setError(err instanceof Error ? err.message : 'Could not open that tag.')
    }
  }

  return (
    <View style={styles.fill}>
      <CameraView
        style={styles.fill}
        barcodeScannerSettings={{ barcodeTypes: ['qr'] }}
        onBarcodeScanned={(e) => void onScanned(e)}
      />
      <View style={styles.overlay}>
        {error ? <Banner tone="bad">{error}</Banner> : null}
        <Note>Point the camera at the QR code on the tag.</Note>
      </View>
    </View>
  )
}

const styles = StyleSheet.create({
  fill: { flex: 1, backgroundColor: '#000' },
  overlay: {
    position: 'absolute',
    left: 16,
    right: 16,
    bottom: 32,
    gap: 8,
    backgroundColor: theme.panel,
    borderRadius: theme.radius,
    padding: theme.pad
  }
})

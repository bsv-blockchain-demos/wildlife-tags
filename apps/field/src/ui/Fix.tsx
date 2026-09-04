/**
 * Taking a position fix, and saying honestly how good it is.
 *
 * The position is the entire scientific value of a report: without it there is
 * no movement study, only a receipt. So this is a first-class step with its own
 * state rather than a silent background read.
 *
 * The accuracy the device claims is shown rather than hidden. Nothing about a
 * signed record proves the phone was where it says it was, and a fix claiming
 * three metres from a phone that has been indoors is worth less than the number
 * suggests. A researcher filtering the dataset needs to see it, and so does the
 * person taking it.
 */
import { useState } from 'react'
import * as Location from 'expo-location'

import { Banner, Button, Field, type BannerTone } from './atoms'

export interface Position {
  lat: number
  lon: number
  accuracyM: number
  at: string
}

export function useFix() {
  const [fix, setFix] = useState<Position | null>(null)
  const [state, setState] = useState<{ tone: BannerTone; text: string }>({
    tone: 'plain',
    text: 'No position fix yet.'
  })
  const [busy, setBusy] = useState(false)

  const take = async () => {
    setBusy(true)
    setState({ tone: 'plain', text: 'Getting a fix…' })
    try {
      const { status } = await Location.requestForegroundPermissionsAsync()
      if (status !== 'granted') {
        setState({
          tone: 'bad',
          text: 'Location permission was refused. Without a position this report has no scientific value, so it cannot be submitted.'
        })
        return
      }
      const pos = await Location.getCurrentPositionAsync({
        accuracy: Location.Accuracy.BestForNavigation
      })
      const next: Position = {
        lat: pos.coords.latitude,
        lon: pos.coords.longitude,
        accuracyM: pos.coords.accuracy ?? 0,
        // The moment of the catch. A queued report submitted hours later still
        // carries this, because it is inside the bytes that get signed.
        at: new Date(pos.timestamp).toISOString().replace(/\.\d{3}Z$/, 'Z')
      }
      setFix(next)
      setState({
        tone: next.accuracyM > 50 ? 'warn' : 'good',
        text:
          `${next.lat.toFixed(5)}, ${next.lon.toFixed(5)} (±${Math.round(next.accuracyM)} m)` +
          (next.accuracyM > 50 ? ' — that is a poor fix. Move into the open and try again.' : '')
      })
    } catch (err) {
      setState({ tone: 'bad', text: `Could not get a fix: ${err instanceof Error ? err.message : String(err)}` })
    } finally {
      setBusy(false)
    }
  }

  return { fix, state, busy, take }
}

export function FixField({
  label,
  fix
}: {
  label: string
  fix: ReturnType<typeof useFix>
}) {
  return (
    <Field label={label}>
      <Banner tone={fix.state.tone}>{fix.state.text}</Banner>
      <Button title={fix.fix ? 'Take a new fix' : 'Use my location'} onPress={fix.take} busy={fix.busy} />
    </Field>
  )
}

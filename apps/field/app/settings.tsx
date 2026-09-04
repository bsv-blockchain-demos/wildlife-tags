/**
 * Which deployment and which chain.
 *
 * A finder never comes here: scanning a tag points the app at whichever DNR
 * issued it. This is for a tagger signing in before they have scanned anything,
 * and for a device pointed at a test deployment.
 */
import { useEffect, useState } from 'react'
import { useRouter } from 'expo-router'

import { Banner, Button, Card, Field, H2, Input, Note, P, Screen } from '../src/ui/atoms'
import * as api from '../src/wildtag/api'
import * as schema from '../src/wildtag/schema'
import * as settings from '../src/wildtag/settings'

export default function Settings() {
  const router = useRouter()
  const [url, setUrl] = useState('')
  const [note, setNote] = useState<{ tone: 'good' | 'bad'; text: string } | null>(null)
  const [busy, setBusy] = useState(false)

  useEffect(() => {
    setUrl(api.server())
  }, [])

  const save = async () => {
    setBusy(true)
    setNote(null)
    try {
      await api.setServer(url.trim())
      // Prove the address is a deployment before accepting it, rather than
      // letting the first real request fail somewhere less obvious.
      const info = await api.info()
      await schema.load(api.server(), true)
      // A deployment on a different network needs a different wallet, which is
      // a different database and a different balance. Say so rather than
      // rebuilding underneath somebody.
      const { changed } = await settings.learnDeployment()
      setNote({
        tone: 'good',
        text:
          `Connected to a ${info.network} deployment paying ` +
          `${info.base_satoshis.toLocaleString()} sats a report.` +
          (changed
            ? ` That is a different network from the last one, so restart the app: ` +
              `your wallet has to be rebuilt for ${info.wallet_chain}.`
            : '')
      })
    } catch (err) {
      setNote({ tone: 'bad', text: err instanceof Error ? err.message : String(err) })
    } finally {
      setBusy(false)
    }
  }

  return (
    <Screen>
      <Card>
        <H2>Deployment</H2>
        <P>The DNR server this phone talks to when it is not following a scanned tag.</P>
        <Field label="Address">
          <Input
            value={url}
            onChangeText={setUrl}
            autoCapitalize="none"
            autoCorrect={false}
            keyboardType="url"
            placeholder="https://tags.dnr.sc.gov"
          />
        </Field>
        {note ? <Banner tone={note.tone}>{note.text}</Banner> : null}
        <Button title="Save and check" onPress={save} busy={busy} />
        <Note>
          Scanning a tag points the app at whichever deployment issued it, so a finder never has to
          set this.
        </Note>
      </Card>
      <Card>
        <Button title="Back" onPress={() => router.back()} tone="quiet" />
      </Card>
    </Screen>
  )
}

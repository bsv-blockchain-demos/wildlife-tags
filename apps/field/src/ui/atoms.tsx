/**
 * The handful of components every screen uses.
 *
 * Deliberately small and unstyled beyond the theme: this is a field tool, and
 * every component here has to be legible at arm's length in sunlight with wet
 * hands. That rules out anything subtle.
 */
import { type ReactNode } from 'react'
import {
  ActivityIndicator,
  Pressable,
  ScrollView,
  StyleSheet,
  Text,
  TextInput,
  View,
  type StyleProp,
  type TextStyle,
  type ViewStyle
} from 'react-native'

import { theme } from './theme'

export function Screen({ children }: { children: ReactNode }) {
  return (
    <ScrollView
      style={styles.screen}
      contentContainerStyle={styles.screenContent}
      keyboardShouldPersistTaps="handled"
    >
      {children}
    </ScrollView>
  )
}

export function Card({ children, style }: { children: ReactNode; style?: StyleProp<ViewStyle> }) {
  return <View style={[styles.card, style]}>{children}</View>
}

export function H1({ children }: { children: ReactNode }) {
  return <Text style={styles.h1}>{children}</Text>
}

export function H2({ children }: { children: ReactNode }) {
  return <Text style={styles.h2}>{children}</Text>
}

export function P({ children, style }: { children: ReactNode; style?: StyleProp<TextStyle> }) {
  return <Text style={[styles.p, style]}>{children}</Text>
}

export function Note({ children }: { children: ReactNode }) {
  return <Text style={styles.note}>{children}</Text>
}

export function Mono({ children }: { children: ReactNode }) {
  return <Text style={styles.mono}>{children}</Text>
}

export type BannerTone = 'plain' | 'good' | 'warn' | 'bad'

export function Banner({ tone = 'plain', children }: { tone?: BannerTone; children: ReactNode }) {
  return (
    <View style={[styles.banner, toneStyle[tone]]}>
      <Text style={styles.bannerText}>{children}</Text>
    </View>
  )
}

export function Button({
  title,
  onPress,
  disabled,
  busy,
  tone = 'primary'
}: {
  title: string
  onPress: () => void
  disabled?: boolean
  busy?: boolean
  tone?: 'primary' | 'quiet' | 'danger'
}) {
  const off = disabled || busy
  return (
    <Pressable
      accessibilityRole="button"
      accessibilityState={{ disabled: !!off, busy: !!busy }}
      onPress={onPress}
      disabled={off}
      style={({ pressed }) => [
        styles.button,
        tone === 'primary' && styles.buttonPrimary,
        tone === 'danger' && styles.buttonDanger,
        off && styles.buttonOff,
        pressed && !off && styles.buttonPressed
      ]}
    >
      {busy ? (
        <ActivityIndicator color={tone === 'primary' ? theme.accentInk : theme.ink} />
      ) : (
        <Text style={[styles.buttonText, tone === 'primary' && styles.buttonTextPrimary]}>{title}</Text>
      )}
    </Pressable>
  )
}

export function Field({
  label,
  help,
  children
}: {
  label: string
  help?: string
  children: ReactNode
}) {
  return (
    <View style={styles.field}>
      <Text style={styles.label}>{label}</Text>
      {children}
      {help ? <Text style={styles.note}>{help}</Text> : null}
    </View>
  )
}

export function Input(props: React.ComponentProps<typeof TextInput>) {
  return <TextInput placeholderTextColor={theme.inkDim} {...props} style={[styles.input, props.style]} />
}

/**
 * Choice is a segmented control rather than a dropdown.
 *
 * A picker on Android opens a modal that a gloved thumb has to dismiss; a row
 * of buttons is one tap. It wraps rather than scrolls, because a hidden option
 * is an option nobody picks.
 */
export function Choice<T extends string>({
  options,
  value,
  onChange
}: {
  options: { code: T; label: string }[]
  value: T | undefined
  onChange: (code: T) => void
}) {
  return (
    <View style={styles.choice}>
      {options.map((o) => {
        const on = o.code === value
        return (
          <Pressable
            key={o.code}
            accessibilityRole="radio"
            accessibilityState={{ selected: on }}
            onPress={() => onChange(o.code)}
            style={({ pressed }) => [
              styles.chip,
              on && styles.chipOn,
              pressed && styles.buttonPressed
            ]}
          >
            <Text style={[styles.chipText, on && styles.chipTextOn]}>{o.label}</Text>
          </Pressable>
        )
      })}
    </View>
  )
}

export function Stat({ n, unit, caption }: { n: string; unit: string; caption: string }) {
  return (
    <View style={styles.stat}>
      <Text style={styles.statN}>{n}</Text>
      <Text style={styles.statU}>{unit}</Text>
      <Text style={styles.statK}>{caption}</Text>
    </View>
  )
}

const toneStyle: Record<BannerTone, ViewStyle> = {
  plain: { borderColor: theme.panelEdge },
  good: { borderColor: theme.good },
  warn: { borderColor: theme.warn },
  bad: { borderColor: theme.bad }
}

const styles = StyleSheet.create({
  screen: { flex: 1, backgroundColor: theme.bg },
  screenContent: { padding: theme.pad, gap: theme.gap, paddingBottom: 48 },
  card: {
    backgroundColor: theme.panel,
    borderRadius: theme.radius,
    borderWidth: 1,
    borderColor: theme.panelEdge,
    padding: theme.pad,
    gap: theme.gap
  },
  h1: { color: theme.ink, fontSize: 26, fontWeight: '700' },
  h2: { color: theme.ink, fontSize: 19, fontWeight: '600' },
  p: { color: theme.ink, fontSize: 16, lineHeight: 23 },
  note: { color: theme.inkDim, fontSize: 13, lineHeight: 19 },
  mono: { color: theme.ink, fontFamily: 'monospace', fontSize: 15 },
  banner: {
    borderWidth: 1,
    borderRadius: 10,
    padding: 12,
    backgroundColor: '#0b1418'
  },
  bannerText: { color: theme.ink, fontSize: 15, lineHeight: 21 },
  button: {
    minHeight: theme.tap,
    borderRadius: 10,
    alignItems: 'center',
    justifyContent: 'center',
    paddingHorizontal: 16,
    borderWidth: 1,
    borderColor: theme.panelEdge,
    backgroundColor: '#182831'
  },
  buttonPrimary: { backgroundColor: theme.accent, borderColor: theme.accent },
  buttonDanger: { borderColor: theme.bad },
  buttonOff: { opacity: 0.45 },
  buttonPressed: { opacity: 0.75 },
  buttonText: { color: theme.ink, fontSize: 16, fontWeight: '600' },
  buttonTextPrimary: { color: theme.accentInk },
  field: { gap: 6 },
  label: { color: theme.ink, fontSize: 15, fontWeight: '600' },
  input: {
    minHeight: theme.tap,
    borderRadius: 10,
    borderWidth: 1,
    borderColor: theme.panelEdge,
    backgroundColor: '#0b1418',
    color: theme.ink,
    paddingHorizontal: 12,
    fontSize: 17
  },
  choice: { flexDirection: 'row', flexWrap: 'wrap', gap: 8 },
  chip: {
    minHeight: theme.tap,
    justifyContent: 'center',
    paddingHorizontal: 14,
    borderRadius: 10,
    borderWidth: 1,
    borderColor: theme.panelEdge,
    backgroundColor: '#0b1418'
  },
  chipOn: { backgroundColor: theme.accent, borderColor: theme.accent },
  chipText: { color: theme.ink, fontSize: 15 },
  chipTextOn: { color: theme.accentInk, fontWeight: '700' },
  stat: { minWidth: 92, gap: 2 },
  statN: { color: theme.accent, fontSize: 26, fontWeight: '700' },
  statU: { color: theme.ink, fontSize: 14 },
  statK: { color: theme.inkDim, fontSize: 12 }
})

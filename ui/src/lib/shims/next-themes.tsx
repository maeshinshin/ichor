interface UseThemeProps {
  themes: string[]
  forcedTheme?: string
  setTheme(theme: string): void
  theme?: string
  resolvedTheme?: string
  systemTheme?: "dark" | "light"
}

const noTheme: UseThemeProps = { setTheme() {}, themes: [] }

export const useTheme = () => noTheme

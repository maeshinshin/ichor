import js from '@eslint/js'
import { globalIgnores } from 'eslint/config'
import tseslint from 'typescript-eslint'

export default tseslint.config(
  globalIgnores(['dist']),
  js.configs.recommended,
  tseslint.configs.recommended,
  {
    // src/lib/radix is preactcn's vendored Radix-for-Preact interop shim
    // (installed by `npx shadcn add`, not hand-written); its generic
    // ref-forwarding needs `any` and isn't ours to restyle.
    files: ['src/lib/radix/**/*.{ts,tsx}'],
    rules: {
      '@typescript-eslint/no-explicit-any': 'off',
    },
  },
)

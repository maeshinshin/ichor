import { render } from 'preact'
import './index.css'
import { App } from './app.tsx'
import { getLang } from './lib/lang.ts'

document.documentElement.lang = getLang()
render(<App />, document.getElementById('app')!)

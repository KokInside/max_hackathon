import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import { MaxUI } from '@maxhub/max-ui'
import '@maxhub/max-ui/dist/styles.css'
import './styles.css'
import { App } from './App'
import { ToastProvider } from './ui'
import { ready } from './bridge'

ready()
createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <MaxUI>
      <ToastProvider>
        <App />
      </ToastProvider>
    </MaxUI>
  </StrictMode>,
)

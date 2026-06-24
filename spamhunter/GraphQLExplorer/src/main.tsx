import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import { Provider } from 'urql'
import { client } from './transport/client'
import { App } from './App'
import './styles/tokens.css'

const rootEl = document.getElementById('root')
if (!rootEl) throw new Error('Root element #root not found')

createRoot(rootEl).render(
  <StrictMode>
    <Provider value={client}>
      <App />
    </Provider>
  </StrictMode>,
)

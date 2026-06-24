import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import './styles/tokens.css'

// App + urql Provider wiring lands in Task 3. For Task 1 the skeleton just
// mounts a React 19 root so the toolchain is provably runnable.
const rootEl = document.getElementById('root')
if (!rootEl) throw new Error('Root element #root not found')

createRoot(rootEl).render(
  <StrictMode>
    <div />
  </StrictMode>,
)

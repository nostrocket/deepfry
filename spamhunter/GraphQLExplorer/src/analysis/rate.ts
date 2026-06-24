// RED-phase stub for the asymmetric posting-rate / burst analyzer (DRILL-01).
// The real interval/burst/bounds math lands in the GREEN phase (Task 2); this stub
// exists only so the test suite compiles and runs RED against the contract.

export interface RateResult {
  analyzedCount: number
  rejectedCount: number
  bins: { start: number; count: number }[]
  burstDetected: boolean
  tightestIntervalSec: number | null
}

export function isSaneTs(_t: number): boolean {
  return false
}

export function analyzeRate(_createdAts: number[]): RateResult {
  return {
    analyzedCount: 0,
    rejectedCount: 0,
    bins: [],
    burstDetected: false,
    tightestIntervalSec: null,
  }
}

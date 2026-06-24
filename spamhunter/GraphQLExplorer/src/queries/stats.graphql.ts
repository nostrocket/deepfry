import { graphql } from '../gql'

// The four fields are exactly contract §5 StatsResult. Typed via the generated
// graphql() fn → data.stats.eventCount is typed `number`.
export const StatsDocument = graphql(`
  query Stats {
    stats {
      eventCount
      maxLevId
      dbVersion
      pinnedStrfryVersion
    }
  }
`)

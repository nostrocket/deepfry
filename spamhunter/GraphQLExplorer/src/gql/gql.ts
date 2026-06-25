/* eslint-disable */
import * as types from './graphql';
import type { TypedDocumentNode as DocumentNode } from '@graphql-typed-document-node/core';

/**
 * Map of all GraphQL operations in the project.
 *
 * This map has several performance disadvantages:
 * 1. It is not tree-shakeable, so it will include all operations in the project.
 * 2. It is not minifiable, so the string of a GraphQL query will be multiple times inside the bundle.
 * 3. It does not support dead code elimination, so it will add unused operations.
 *
 * Therefore it is highly recommended to use the babel or swc plugin for production.
 * Learn more about it here: https://the-guild.dev/graphql/codegen/plugins/presets/preset-client#reducing-bundle-size
 */
type Documents = {
    "\n  query Authors($after: String, $limit: Int) {\n    authors(after: $after, limit: $limit) {\n      authors\n      endCursor\n      hasMore\n    }\n  }\n": typeof types.AuthorsDocument,
    "\n  query Events($filter: EventFilterInput, $after: String, $limit: Int) {\n    events(filter: $filter, after: $after, limit: $limit) {\n      events {\n        id\n        pubkey\n        kind\n        createdAt\n        content\n        tags\n      }\n      endCursor\n      hasMore\n    }\n  }\n": typeof types.EventsDocument,
    "\n  query LatestPerAuthor($kind: Int!, $perAuthor: Int!, $authors: [String!]!) {\n    latestPerAuthor(kind: $kind, perAuthor: $perAuthor, authors: $authors) {\n      author\n      events {\n        id\n        pubkey\n        kind\n        createdAt\n        content\n        tags\n      }\n    }\n  }\n": typeof types.LatestPerAuthorDocument,
    "\n  query RawEvent($filter: EventFilterInput, $limit: Int) {\n    events(filter: $filter, limit: $limit) {\n      events {\n        id\n        raw\n      }\n    }\n  }\n": typeof types.RawEventDocument,
    "\n  query Stats {\n    stats {\n      eventCount\n      maxLevId\n      dbVersion\n      pinnedStrfryVersion\n    }\n  }\n": typeof types.StatsDocument,
};
const documents: Documents = {
    "\n  query Authors($after: String, $limit: Int) {\n    authors(after: $after, limit: $limit) {\n      authors\n      endCursor\n      hasMore\n    }\n  }\n": types.AuthorsDocument,
    "\n  query Events($filter: EventFilterInput, $after: String, $limit: Int) {\n    events(filter: $filter, after: $after, limit: $limit) {\n      events {\n        id\n        pubkey\n        kind\n        createdAt\n        content\n        tags\n      }\n      endCursor\n      hasMore\n    }\n  }\n": types.EventsDocument,
    "\n  query LatestPerAuthor($kind: Int!, $perAuthor: Int!, $authors: [String!]!) {\n    latestPerAuthor(kind: $kind, perAuthor: $perAuthor, authors: $authors) {\n      author\n      events {\n        id\n        pubkey\n        kind\n        createdAt\n        content\n        tags\n      }\n    }\n  }\n": types.LatestPerAuthorDocument,
    "\n  query RawEvent($filter: EventFilterInput, $limit: Int) {\n    events(filter: $filter, limit: $limit) {\n      events {\n        id\n        raw\n      }\n    }\n  }\n": types.RawEventDocument,
    "\n  query Stats {\n    stats {\n      eventCount\n      maxLevId\n      dbVersion\n      pinnedStrfryVersion\n    }\n  }\n": types.StatsDocument,
};

/**
 * The graphql function is used to parse GraphQL queries into a document that can be used by GraphQL clients.
 *
 *
 * @example
 * ```ts
 * const query = graphql(`query GetUser($id: ID!) { user(id: $id) { name } }`);
 * ```
 *
 * The query argument is unknown!
 * Please regenerate the types.
 */
export function graphql(source: string): unknown;

/**
 * The graphql function is used to parse GraphQL queries into a document that can be used by GraphQL clients.
 */
export function graphql(source: "\n  query Authors($after: String, $limit: Int) {\n    authors(after: $after, limit: $limit) {\n      authors\n      endCursor\n      hasMore\n    }\n  }\n"): (typeof documents)["\n  query Authors($after: String, $limit: Int) {\n    authors(after: $after, limit: $limit) {\n      authors\n      endCursor\n      hasMore\n    }\n  }\n"];
/**
 * The graphql function is used to parse GraphQL queries into a document that can be used by GraphQL clients.
 */
export function graphql(source: "\n  query Events($filter: EventFilterInput, $after: String, $limit: Int) {\n    events(filter: $filter, after: $after, limit: $limit) {\n      events {\n        id\n        pubkey\n        kind\n        createdAt\n        content\n        tags\n      }\n      endCursor\n      hasMore\n    }\n  }\n"): (typeof documents)["\n  query Events($filter: EventFilterInput, $after: String, $limit: Int) {\n    events(filter: $filter, after: $after, limit: $limit) {\n      events {\n        id\n        pubkey\n        kind\n        createdAt\n        content\n        tags\n      }\n      endCursor\n      hasMore\n    }\n  }\n"];
/**
 * The graphql function is used to parse GraphQL queries into a document that can be used by GraphQL clients.
 */
export function graphql(source: "\n  query LatestPerAuthor($kind: Int!, $perAuthor: Int!, $authors: [String!]!) {\n    latestPerAuthor(kind: $kind, perAuthor: $perAuthor, authors: $authors) {\n      author\n      events {\n        id\n        pubkey\n        kind\n        createdAt\n        content\n        tags\n      }\n    }\n  }\n"): (typeof documents)["\n  query LatestPerAuthor($kind: Int!, $perAuthor: Int!, $authors: [String!]!) {\n    latestPerAuthor(kind: $kind, perAuthor: $perAuthor, authors: $authors) {\n      author\n      events {\n        id\n        pubkey\n        kind\n        createdAt\n        content\n        tags\n      }\n    }\n  }\n"];
/**
 * The graphql function is used to parse GraphQL queries into a document that can be used by GraphQL clients.
 */
export function graphql(source: "\n  query RawEvent($filter: EventFilterInput, $limit: Int) {\n    events(filter: $filter, limit: $limit) {\n      events {\n        id\n        raw\n      }\n    }\n  }\n"): (typeof documents)["\n  query RawEvent($filter: EventFilterInput, $limit: Int) {\n    events(filter: $filter, limit: $limit) {\n      events {\n        id\n        raw\n      }\n    }\n  }\n"];
/**
 * The graphql function is used to parse GraphQL queries into a document that can be used by GraphQL clients.
 */
export function graphql(source: "\n  query Stats {\n    stats {\n      eventCount\n      maxLevId\n      dbVersion\n      pinnedStrfryVersion\n    }\n  }\n"): (typeof documents)["\n  query Stats {\n    stats {\n      eventCount\n      maxLevId\n      dbVersion\n      pinnedStrfryVersion\n    }\n  }\n"];

export function graphql(source: string) {
  return (documents as any)[source] ?? {};
}

export type DocumentType<TDocumentNode extends DocumentNode<any, any>> = TDocumentNode extends DocumentNode<  infer TType,  any>  ? TType  : never;
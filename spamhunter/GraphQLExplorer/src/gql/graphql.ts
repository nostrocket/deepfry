/* eslint-disable */
/** Internal type. DO NOT USE DIRECTLY. */
type Exact<T extends { [key: string]: unknown }> = { [K in keyof T]: T[K] };
/** Internal type. DO NOT USE DIRECTLY. */
export type Incremental<T> = T | { [P in keyof T]?: P extends ' $fragmentName' | '__typename' ? T[P] : never };
import type { TypedDocumentNode as DocumentNode } from '@graphql-typed-document-node/core';
export type EventFilterInput = {
  authors?: Array<string> | null | undefined;
  ids?: Array<string> | null | undefined;
  kinds?: Array<number> | null | undefined;
  since?: number | null | undefined;
  tag?: TagFilterInput | null | undefined;
  until?: number | null | undefined;
};

export type TagFilterInput = {
  name: string;
  values: Array<string>;
};

export type EventsQueryVariables = Exact<{
  filter?: EventFilterInput | null | undefined;
  after?: string | null | undefined;
  limit?: number | null | undefined;
}>;


export type EventsQuery = { events: { endCursor: string | null, hasMore: boolean, events: Array<{ id: string, pubkey: string, kind: number, createdAt: number, content: string }> } };

export type StatsQueryVariables = Exact<{ [key: string]: never; }>;


export type StatsQuery = { stats: { eventCount: number, maxLevId: number, dbVersion: number, pinnedStrfryVersion: string } };


export const EventsDocument = {"kind":"Document","definitions":[{"kind":"OperationDefinition","operation":"query","name":{"kind":"Name","value":"Events"},"variableDefinitions":[{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"filter"}},"type":{"kind":"NamedType","name":{"kind":"Name","value":"EventFilterInput"}}},{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"after"}},"type":{"kind":"NamedType","name":{"kind":"Name","value":"String"}}},{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"limit"}},"type":{"kind":"NamedType","name":{"kind":"Name","value":"Int"}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"events"},"arguments":[{"kind":"Argument","name":{"kind":"Name","value":"filter"},"value":{"kind":"Variable","name":{"kind":"Name","value":"filter"}}},{"kind":"Argument","name":{"kind":"Name","value":"after"},"value":{"kind":"Variable","name":{"kind":"Name","value":"after"}}},{"kind":"Argument","name":{"kind":"Name","value":"limit"},"value":{"kind":"Variable","name":{"kind":"Name","value":"limit"}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"events"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"id"}},{"kind":"Field","name":{"kind":"Name","value":"pubkey"}},{"kind":"Field","name":{"kind":"Name","value":"kind"}},{"kind":"Field","name":{"kind":"Name","value":"createdAt"}},{"kind":"Field","name":{"kind":"Name","value":"content"}}]}},{"kind":"Field","name":{"kind":"Name","value":"endCursor"}},{"kind":"Field","name":{"kind":"Name","value":"hasMore"}}]}}]}}]} as unknown as DocumentNode<EventsQuery, EventsQueryVariables>;
export const StatsDocument = {"kind":"Document","definitions":[{"kind":"OperationDefinition","operation":"query","name":{"kind":"Name","value":"Stats"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"stats"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"eventCount"}},{"kind":"Field","name":{"kind":"Name","value":"maxLevId"}},{"kind":"Field","name":{"kind":"Name","value":"dbVersion"}},{"kind":"Field","name":{"kind":"Name","value":"pinnedStrfryVersion"}}]}}]}}]} as unknown as DocumentNode<StatsQuery, StatsQueryVariables>;
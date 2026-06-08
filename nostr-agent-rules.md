# Nostr Agent Rules

Distilled from `nostr-patterns.md`. Each rule is a language-agnostic principle with concrete examples in the languages this project actually uses (Go via `go-nostr`, TypeScript via `nostr-tools`). The rules apply to React Native, Node, browser, and server contexts equally.

---

## Time, Identity, Determinism

### 1. Use the library's timestamp helper — never raw milliseconds

Nostr `created_at` is Unix **seconds**. Most language runtimes default to milliseconds, and a value 1000× too large lands events ~50,000 years in the future, where relays silently discard them. Always go through the library's helper or an explicit `/1000`.

❌ Bad
```ts
{ created_at: Date.now() }                          // milliseconds
```
```go
event.CreatedAt = nostr.Timestamp(time.Now().UnixMilli())
```

✅ Good
```ts
{ created_at: Math.floor(Date.now() / 1000) }
```
```go
event.CreatedAt = nostr.Now()
```

---

### 2. Never compute or set the event ID and signature manually

The event ID is SHA-256 of a canonical **array** with strict field order and no whitespace. Object key order in JS, Go map iteration, and most JSON encoders make hand-rolled IDs non-deterministic. Use the library's signing entry point exclusively.

❌ Bad
```ts
const id = sha256(JSON.stringify({ pubkey, created_at, kind, tags, content }));
```
```go
event.ID = myCustomHash(event)
event.Sig = mySign(event.ID)
```

✅ Good
```ts
const event = finalizeEvent(template, sk);            // sets id + sig
```
```go
event.Sign(skHex)                                     // sets ID + Sig
```

---

### 3. Verify signatures on every incoming event before acting on it

A relay can serve any bytes — including forged pubkeys and tampered follow lists. Verify in every read path: render, store, forward, score. This applies especially to kind 3 and other replaceable kinds whose content drives downstream trust decisions.

❌ Bad
```ts
ws.onmessage = ({ data }) => {
  const [, , event] = JSON.parse(data);
  store.insert(event);                                // trusts the wire
};
```

✅ Good
```ts
if (!verifyEvent(event)) return;
store.insert(event);
```
```go
if ok, err := event.CheckSignature(); !ok || err != nil {
  continue
}
```

---

## Key Material

### 4. Never embed private keys in client bundles or use weak randomness

Anything imported by a frontend bundle ends up in DevTools, source maps, and CDN caches. Use platform signing primitives (NIP-07 in browsers, NIP-46 on servers) so the secret never enters app memory. For key generation, use the OS CSPRNG; `Math.random` and equivalents are not cryptographic.

❌ Bad
```ts
const PRIVATE_KEY = "nsec1abc...";                    // shipped to every visitor
const k = Array.from({length: 32}, () => Math.floor(Math.random() * 256));
```

✅ Good
```ts
const signed = await window.nostr.signEvent(template); // browser extension
const sk = generateSecretKey();                        // crypto.getRandomValues
```

---

### 5. Accept both bech32 and hex key formats at user-facing boundaries

Users paste keys in either form (`nsec1…` / `npub1…` or raw hex). Normalize once at the boundary so internal code only deals with hex.

❌ Bad
```go
sk := os.Getenv("KEY")                                // crashes on nsec1...
event.Sign(sk)
```

✅ Good
```go
func decodeSecretKey(raw string) (string, error) {
  if strings.HasPrefix(raw, "nsec") {
    prefix, data, err := nip19.Decode(raw)
    if err != nil || prefix != "nsec" { return "", err }
    return data.(string), nil
  }
  return raw, nil
}
```

---

## Publishing

### 6. Distinguish permanent rejections from transient failures

Relays signal permanent rejection with prefixes (`blocked:`, `invalid:`, `duplicate:`, `pow:`, `restricted:`, `auth-required:`). Retrying these wastes bandwidth and risks IP bans. Retry only network/timeout failures, with exponential backoff and jitter.

❌ Bad
```ts
while (true) {
  if ((await publish(event)) === "ok") break;
  await delay(1000);                                  // hammers blocked events
}
```

✅ Good
```ts
const PERMANENT = ["duplicate:", "blocked:", "invalid:", "pow:", "restricted:"];
const result = await publish(event);
if (result === "rejected") throw new Error("permanent");
if (result === "failed") await retryWithBackoff();
```

---

### 7. Match the event ID in every OK/ack response

A single connection carries concurrent publishes. A handler that resolves "the next OK" will resolve the wrong promise on out-of-order or batched responses.

❌ Bad
```ts
ws.send(["EVENT", a]); ws.send(["EVENT", b]);
ws.onmessage = ({data}) => {
  const [type, , ok] = JSON.parse(data);
  if (type === "OK") resolve(ok);                     // for which event?
};
```

✅ Good
```ts
const handler = ({data}) => {
  const [type, id, ok] = JSON.parse(data);
  if (type === "OK" && id === event.id) resolve(ok ? "ok" : "rejected");
};
```

---

### 8. Publish replaceable events oldest-first, sequentially per author

Replaceable kinds (0, 3, 10000–19999, 30000–39999) follow last-write-wins. If a stale version arrives after a fresh one, it silently overwrites. Sort ascending by `created_at` and serialize publishes within a pubkey. Parallelize across pubkeys, never within.

❌ Bad
```go
for _, evt := range events {                          // arbitrary order
  go relay.Publish(ctx, evt)                          // races; stale wins
}
```

✅ Good
```go
sort.Slice(events, func(i, j int) bool { return events[i].CreatedAt < events[j].CreatedAt })
for _, evt := range events {
  relay.Publish(ctx, evt)                             // sequential per pubkey
}
```

---

## Subscriptions

### 9. Always bound historical queries

An unbounded REQ on a busy relay delivers tens of thousands of events. Always include `limit` plus a narrowing constraint (`authors`, `kinds`, `since`/`until`).

❌ Bad
```ts
ws.send(["REQ", id, { kinds: [1] }]);                 // every text note ever
```

✅ Good
```ts
ws.send(["REQ", id, {
  kinds: [1],
  authors: followedPubkeys,
  since: Math.floor(Date.now() / 1000) - 86400,
  limit: 200,
}]);
```

---

### 10. Batch authors into one filter — don't open a subscription per author

Relay subscription caps are typically 10–50 per connection. One REQ per followed user hits the cap immediately, and most relays drop later REQs silently.

❌ Bad
```ts
for (const pk of follows) {
  ws.send(["REQ", pk.slice(0, 8), { kinds: [1], authors: [pk] }]);
}
```

✅ Good
```ts
ws.send(["REQ", "feed", { kinds: [1], authors: follows, limit: 200 }]);
```

---

### 11. Use unique subscription IDs and close one-shot queries after EOSE

Reusing an ID silently replaces the prior filter without error. Forgetting to close a one-shot REQ leaks a slot until the connection drops.

❌ Bad
```ts
ws.send(["REQ", "sub", filterA]);
ws.send(["REQ", "sub", filterB]);                     // filterA gone, no warning
```

✅ Good
```ts
const id = crypto.randomUUID().slice(0, 12);
ws.send(["REQ", id, filter]);
ws.onmessage = ({data}) => {
  const [type, sid] = JSON.parse(data);
  if (type === "EOSE" && sid === id) ws.send(["CLOSE", id]);
};
```
```go
defer sub.Unsub()                                     // go-nostr equivalent
```

---

### 12. Use EOSE as the historical → live boundary

`["EOSE", id]` signals stored events are exhausted. Events after EOSE are live. Treating both alike races initial render and loses chronological ordering during replay.

❌ Bad
```ts
ws.onmessage = ({data}) => {
  const [type, , event] = JSON.parse(data);
  if (type === "EVENT") render(event);                // re-renders during replay
};
```

✅ Good
```ts
let live = false, buffer = [];
ws.onmessage = ({data}) => {
  const msg = JSON.parse(data);
  if (msg[0] === "EVENT") live ? handleLive(msg[2]) : buffer.push(msg[2]);
  if (msg[0] === "EOSE") { live = true; render(buffer.sort(byCreatedAt)); }
};
```

---

## Connection Management

### 13. Pool one connection per relay URL — and normalize the URL first

Different parts of an app reaching the same relay should share a connection. Without normalization, `wss://Relay.Example.com/` and `wss://relay.example.com` look like two relays — doubling subscription count, breaking dedupe, and exhausting limits.

❌ Bad
```ts
const wsForFeed    = new WebSocket(url);
const wsForProfile = new WebSocket(url);              // same relay, two sockets
```

✅ Good
```ts
function getConn(url: string) {
  const key = normalizeRelayUrl(url);
  if (!pool.has(key)) pool.set(key, openConnection(key));
  return pool.get(key)!;
}
```
```go
key := nostr.NormalizeURL(rawURL)                     // before using as map key
```

---

### 14. Reconnect with exponential backoff and jitter; replay active subscriptions

Most Nostr libraries (including `go-nostr`) do not auto-reconnect. After disconnect, re-dial with backoff capped at ~1 minute, add `Math.random() * 500ms` jitter to avoid thundering herds when a relay restarts, and resend every active REQ — the server has no memory of prior subscriptions.

❌ Bad
```ts
ws.onclose = () => new WebSocket(url);                // tight loop, no backoff
ws.onopen = () => { /* nothing */ };                  // feed silently dies
```

✅ Good
```ts
ws.onclose = () => {
  const delay = Math.min(1000 * 2 ** attempt, 60_000) + Math.random() * 500;
  setTimeout(connect, delay);
};
ws.onopen = () => {
  for (const [id, filters] of activeSubs) ws.send(JSON.stringify(["REQ", id, ...filters]));
};
```

---

### 15. Cap reconnect attempts; remove dead relays — but maintain a minimum pool

A relay that fails 5+ times consecutively is dead, not slow. Stop retrying. But never let the pool fall below ~3 relays — a slow relay beats no relay.

❌ Bad
```go
for { connect(url) }                                  // infinite retry
if latency > 2000 { removeRelay(url) }                // single bad sample
```

✅ Good
```go
if state.failures >= 5 { pool.Remove(url); return }
state.backoff = min(state.backoff * 2, 5 * time.Minute)
healthy := filter(pool, isHealthy)
if len(healthy) < 3 { healthy = topNBySore(pool, 3) }
```

---

## Discovery & Routing

### 16. Respect NIP-65 read/write markers; have a hardcoded seed fallback

Kind 10002 tags carry `"read"`, `"write"`, or no marker (both). Publishing to a read-only relay wastes the round-trip; querying a write-only relay returns nothing. New users may have no kind 10002 yet — fall back to kind 3 `content` JSON, then to a hardcoded seed list.

❌ Bad
```ts
const all = ev.tags.filter(t => t[0] === "r").map(t => t[1]);
await publish(myEvent, all);                          // ignores markers
```

✅ Good
```ts
for (const tag of event.tags) {
  if (tag[0] !== "r") continue;
  const marker = tag[2];
  if (!marker || marker === "write") write.push(tag[1]);
  if (!marker || marker === "read")  read.push(tag[1]);
}
```

---

## Boundary I/O — Pipes, Plugins, Forwarders

### 17. Enlarge default I/O buffer sizes when reading external events

Default scanner/reader buffers (Go's `bufio.Scanner` is 64KB) silently truncate large events — long-form articles, kind 3 lists with thousands of tags, profile metadata. The downstream system may then interpret silence as accept, defeating filters.

❌ Bad
```go
scanner := bufio.NewScanner(os.Stdin)                 // 64KB cap
```

✅ Good
```go
scanner := bufio.NewScanner(os.Stdin)
scanner.Buffer(make([]byte, 10<<20), 10<<20)          // 10MB
```

---

### 18. Preserve raw event bytes when forwarding — never round-trip decode/encode

Re-marshalling can change whitespace, escape sequences, or field order. The result is a different SHA-256, a different event ID, and a broken signature. Hold the original bytes verbatim when you intend to forward.

❌ Bad
```go
type Input struct { Event Event `json:"event"` }      // decoded → re-encoded
forward(json.Marshal(input.Event))
```

✅ Good
```go
type Input struct { Event json.RawMessage `json:"event"` }  // verbatim bytes
forward(input.Event)
```

---

### 19. Fail closed on trust/policy lookup errors

When a whitelist, allowlist, or signature check cannot complete (timeout, network error, dependency down), reject. Accepting on error defeats the entire purpose of the gate.

❌ Bad
```go
ok, err := whitelist.IsAllowed(pk)
if err != nil { return Accept }                       // permits anyone when down
return ok
```

✅ Good
```go
ok, err := whitelist.IsAllowed(pk)
if err != nil { return Reject("whitelist check failed") }
return ok
```

---

### 20. Never block the hot path — drop instead

A protocol handler blocking on a downstream service (DB write, relay publish) will time out the upstream caller. Enqueue onto a buffered channel/queue with a non-blocking send that drops on overflow.

❌ Bad
```go
func (p *Plugin) Handle(evt Event) {
  p.publisher.PublishSync(evt)                        // blocks the stdin handler
}
```

✅ Good
```go
func (p *Publisher) Enqueue(evt json.RawMessage) {
  select {
  case p.queue <- evt:                                // ok
  default:                                            // queue full — drop
    p.dropped.Add(1)
  }
}
```

---

## Cross-Cutting Hygiene

### 21. Deduplicate by event ID with a bounded LRU set

The same event arrives from every subscribed relay. Process each ID once. Unbounded sets leak memory under sustained load; use a fixed-size LRU.

❌ Bad
```ts
const seen = new Set<string>();                       // grows forever
if (!seen.has(e.id)) { seen.add(e.id); process(e); }
```

✅ Good
```ts
const seen = new LruSet<string>(10_000);
if (!seen.has(e.id)) { seen.add(e.id); process(e); }
```

---

### 22. Sort by `created_at`, never by arrival order

Stored events arrive in arbitrary order; multi-relay fanout makes it worse. Always sort by `created_at` before rendering or processing as a sequence.

❌ Bad
```ts
events.push(incoming);
render(events);                                       // jumbled timeline
```

✅ Good
```ts
render([...events].sort((a, b) => b.created_at - a.created_at));
```

---

### 23. Cache NIP-11 limits; respect them when batching

Each relay advertises `max_subscriptions`, `max_filters`, `max_message_length`, `max_limit`. Hardcoded batch sizes silently exceed some relays' caps. Fetch `application/nostr+json` on first connect and cache.

❌ Bad
```ts
sendBatch(authors.slice(0, 1000));                    // some relays cap at 100
```

✅ Good
```ts
const info = await fetchNip11(url);
const cap = info.limitation?.max_filters ?? 10;
for (const chunk of chunks(authors, cap)) sendBatch(chunk);
```

---

### 24. Treat `NOTICE` as informational, not as a protocol error

`["NOTICE", "..."]` is a free-form string the relay sends for debugging or human-readable messages. Don't disconnect, throw, or alter subscription state on receipt — log and continue.

❌ Bad
```ts
if (msg[0] === "NOTICE") throw new Error(msg[1]);     // tears down working sub
```

✅ Good
```ts
if (msg[0] === "NOTICE") console.debug(`[${url}] ${msg[1]}`);
```

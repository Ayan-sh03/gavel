# Auction Backend Spec (portable and simple)

This spec describes a backend for an auction site where people list items and others bid. It uses plain HTTP, any SQL database, and runs on one or many servers. Words are simple; rules are clear.

## Goals
- People list items for sale and start an auction.
- People bid. Highest valid bid wins when time runs out.
- Real-time updates for bids and time changes.
- Safe under heavy traffic and race conditions.
- Works on any platform (Windows/Linux/Mac), any cloud, any SQL DB (Postgres/MySQL/SQLite). No vendor lock.

## Non-goals (MVP)
- No complex auto-bidding (proxy) in v1.
- No multi-currency per auction (choose one per auction).
- No fancy search; just simple filters.

---

## Core Auction Rules
- Type: English (price goes up). One item per auction (v1).
- Status flow: draft → scheduled → live → ending (internal) → ended (won/unsold) or canceled.
- Start/End time in UTC.
- Bids must be: > current price AND ≥ current price + min increment.
- Tie: earlier bid wins if two bids have the same amount (API blocks equal amount, so ties are rare).
- Reserve price (optional): if not met at end, auction is “unsold”.
- Anti-sniping: if a bid arrives in the last X seconds (extension window), push the end time by X seconds from the bid time.
- Seller cannot bid on their own auction.
- No bid deletion. Admin can cancel auctions by policy.

---

## Data Model (SQL-friendly)
Use integer cents for money. Use server/DB time (UTC). IDs can be UUID strings.

Table: users
- id (string)
- email (string, unique)
- password_hash (string)
- display_name (string)
- role (enum: user, admin) default user
- status (enum: active, banned) default active
- created_at, updated_at (timestamp)
Indexes: (email unique)

Table: listings
- id (string)
- seller_id (string → users.id)
- title (string)
- description (text)
- category (string)
- condition (enum: new, used)
- cover_image_url (string) [or separate images table]
- created_at, updated_at (timestamp)
Indexes: (seller_id), (created_at desc)

Table: listing_images
- id (string)
- listing_id (string)
- url (string)
- position (int)
Index: (listing_id, position)

Table: auctions
- id (string)
- listing_id (string → listings.id)
- status (enum: draft, scheduled, live, ending, ended, canceled)
- currency (string, e.g., USD)
- starting_price_cents (int)
- reserve_price_cents (int, nullable)
- min_increment_cents (int)
- starts_at (timestamp UTC)
- ends_at (timestamp UTC)
- extension_window_secs (int, default 120–300)
- current_price_cents (int, nullable)
- current_winner_id (string, nullable)
- bid_count (int, default 0)
- created_at, updated_at (timestamp)
Indexes: (status, starts_at), (status, ends_at), (listing_id), (current_winner_id)

Table: bids
- id (string)
- auction_id (string)
- bidder_id (string)
- amount_cents (int)
- placed_at (timestamp)
- client_key (string, nullable) — for retry-safety (Idempotency-Key)
- accepted (bool) — true if it became the current price
- source_ip (string, nullable)
Indexes: (auction_id, placed_at desc), (auction_id, amount_cents desc), unique(auction_id, client_key) where client_key not null

Table: watchlists
- user_id (string)
- auction_id (string)
PK: (user_id, auction_id)

Table: payments (winner → site)
- id (string)
- auction_id, winner_id (string)
- amount_cents (int)
- status (enum: pending, paid, failed, refunded)
- provider (string), provider_ref (string, nullable)
- created_at, updated_at
Indexes: (winner_id), (auction_id)

Table: payouts (site → seller)
- id (string)
- seller_id, auction_id (string)
- amount_cents (int)
- status (enum: pending, paid, failed)
- created_at, updated_at
Indexes: (seller_id), (auction_id)

Table: audit_log
- id (string)
- actor_id (string, nullable)
- action (string) e.g., bid_placed, auction_finalized
- subject_type (string), subject_id (string)
- meta_json (text)
- created_at
Index: (subject_type, subject_id, created_at)

---

## API (HTTP JSON)
Auth: bearer token (JWT) or session cookie. All times UTC.

Auth
- POST /auth/register {email, password, display_name}
- POST /auth/login {email, password} → {token}
- POST /auth/logout

Users
- GET /me → profile

Listings
- POST /listings (auth: seller)
- GET /listings?query=&category=&seller_id=&page=&sort=
- GET /listings/{id}
- PATCH /listings/{id} (seller or admin)
- POST /listings/{id}/images (upload URL handling TBD)

Auctions
- POST /auctions (from listing_id) → schedules or starts
- GET /auctions?status=&category=&ends_before=&page=&sort=
- GET /auctions/{id}
- PATCH /auctions/{id} (admin only for cancel or fix data)
- POST /auctions/{id}/cancel (seller before first bid or admin)

Bids
- GET  /auctions/{id}/bids?limit=50&before=
- POST /auctions/{id}/bids
  Headers: Idempotency-Key (optional but recommended)
  Body: {amount_cents}
  Responses:
  - 201: {accepted: true, current_price_cents, ends_at, bid_count}
  - 409: {accepted: false, reason: "outbid/new_min", current_price_cents, next_min_cents, ends_at}
  - 422: invalid input; 403: forbidden (seller or banned); 410: auction ended

Watchlist
- POST /auctions/{id}/watch
- DELETE /auctions/{id}/watch
- GET /me/watchlist

Payments
- POST /auctions/{id}/pay (winner only)
- GET /payments/{id}

Admin
- GET /admin/auctions?status=
- POST /admin/auctions/{id}/cancel

Live Updates
- GET /auctions/{id}/stream (SSE) — sends events: bid_placed, time_extended, auction_ended

Errors
- Use standard codes with {message, code} JSON.

---

## Bidding: safe single-step write
Always use DB time (now from DB). Do not trust client time.

Rules checked in one transaction:
1) Auction is live and now < ends_at.
2) Bidder != seller; user active.
3) amount_cents ≥ current_min (current price or start) + min_increment.
4) If reserve set, track if met.

SQL-style update (generic):
```sql
-- Pseudocode; adjust to your SQL dialect
BEGIN;
  SELECT * FROM auctions WHERE id = :aid FOR UPDATE; -- lock row
  -- compute next_min = COALESCE(current_price_cents, starting_price_cents) + min_increment_cents
  IF auction.status != 'live' OR now() >= auction.ends_at THEN ROLLBACK; RETURN 410; END IF;
  IF :user_id = seller_id THEN ROLLBACK; RETURN 403; END IF;
  IF :amount < next_min THEN ROLLBACK; RETURN 409 WITH next_min; END IF;

  -- Optional anti-snipe extension
  IF auction.ends_at - now() <= auction.extension_window_secs THEN
    auction.ends_at := GREATEST(auction.ends_at, now() + auction.extension_window_secs);
  END IF;

  INSERT INTO bids(id, auction_id, bidder_id, amount_cents, placed_at, client_key, accepted)
    VALUES(:bid_id, :aid, :user_id, :amount, now(), :idemp_key, TRUE);

  UPDATE auctions
    SET current_price_cents = :amount,
        current_winner_id = :user_id,
        bid_count = bid_count + 1,
        ends_at = auction.ends_at,
        updated_at = now()
    WHERE id = :aid;
COMMIT;
```
If another bidder commits first, the lock will serialize. The second bidder re-checks and returns 409 with the new next_min.

Idempotency (safe retry):
- Client sends header Idempotency-Key for POST /bids.
- Store in bids.client_key with unique (auction_id, client_key). On conflict, return the first result.

---

## Ending Auctions (background job)
- A background job runs every second (or uses a timer) and picks due auctions.
- Claim one auction with a status swap so only one worker finalizes it.

SQL-style claim and finalize:
```sql
-- Claim one
UPDATE auctions
  SET status = 'ending'
WHERE id = (
  SELECT id FROM auctions
  WHERE status = 'live' AND ends_at <= now()
  ORDER BY ends_at
  LIMIT 1
  FOR UPDATE SKIP LOCKED
)
RETURNING id;

-- Finalize (in a tx)
BEGIN;
  SELECT * FROM auctions WHERE id = :aid FOR UPDATE;
  IF current_winner_id IS NULL OR (reserve_price_cents IS NOT NULL AND current_price_cents < reserve_price_cents) THEN
    UPDATE auctions SET status='ended' WHERE id=:aid; -- unsold
  ELSE
    UPDATE auctions SET status='ended' WHERE id=:aid; -- sold
    INSERT INTO payments(id, auction_id, winner_id, amount_cents, status, created_at)
      VALUES(:pay_id, :aid, current_winner_id, current_price_cents, 'pending', now());
    INSERT INTO payouts(id, auction_id, seller_id, amount_cents, status, created_at)
      VALUES(:payout_id, :aid, seller_id, calc_seller_amount, 'pending', now());
  END IF;
COMMIT;
```
- After commit, send notifications: winner, seller, watchers.
- If job crashes, the auction stays not-ended; the next run will pick it up again.

---

## Real-time Updates
- Server-Sent Events (SSE) endpoint: GET /auctions/{id}/stream
  - Events: bid_placed, time_extended, auction_ended
  - Works over plain HTTP, no special proxy needed. Use keep-alive.
- Optional: WebSocket variant if needed later.

---

## Caching
- Cache hot reads (auction details, top bids) for a few seconds.
- Invalidate cache on bid placement and status change.
- If no cache server is present, run without it.

---

## Payments and Payouts (simple flow)
- When auction ends with a winner, create a payment record (pending).
- Winner pays on POST /auctions/{id}/pay; integrate any provider later.
- After money is captured, mark payout to seller as pending; settle via manual or provider API.

---

## Security, Abuse, and Limits
- Validate all inputs; use server/DB time only.
- Rate limit POST /bids per user and per IP.
- Prevent seller from bidding on own auction.
- Basic email verification before bidding.
- Audit log for bids and status changes.

---

## Deployment and Portability
- Stateless API servers (can run 1 or many).
- One background worker process for end-of-auction jobs (can scale to many safely due to row locking/claiming or a simple lease flag).
- Any SQL DB. Start with SQLite for dev; Postgres/MySQL for prod. If DB lacks SKIP LOCKED (e.g., SQLite), use compare-and-swap on status + updated_at.
- Config via env: DB_URL, JWT_SECRET, MAIL_*, CACHE_URL (optional), PAYMENT_* (optional).
- Run as a simple process, container, or serverless (if serverless, the background job runs as a scheduled function).

### Scale Plan (when traffic grows)
- Read replicas for GET APIs; writes always go to primary.
- Shard auctions by id prefix if needed (later).
- Cache hot auctions with short TTL.
- Batch notifications and emails.
- Keep bid path fast and single-transaction.

---

## Testing Plan
Unit
- Min increment, next_min math.
- Reserve met/unmet.
- Anti-snipe extension window math.

Race/Load
- Many concurrent bids on same auction → exactly one accepted per price step; no skipped steps; ends_at extends correctly.
- Bids at the exact end time (reject when now >= ends_at).

E2E
- Create listing → create auction → place bids → extend → end → winner pays → seller payout queued.

---

## Build and Test Process (TDD, one test at a time)
- We use TDD only. No code without a failing test first.
- Work in small steps:
  1) Pick one small behavior.
  2) Write one test that fails.
  3) Run only that test.
  4) Write the least code to make it pass.
  5) Run only that test until green.
  6) Clean up (refactor) without changing behavior.
  7) Repeat.
- Do not run the whole test suite during these steps. Run the whole suite only in CI and before merge/release.
- Run a single test (examples; final scripts depend on chosen language):
  - Node (Jest/Vitest): `npm run test:one -- -t "name" [path]`
  - Python (pytest): `pytest tests/path.py::TestClass::test_name -q`
  - Go: `go test -run '^TestName$' ./module`
- Provide a unified script `test:one` that takes a name or path; `test:all` runs the full suite.
- Tests are small, fast, and independent. Concurrency cases (like bids) use a single focused test that drives many workers inside it.

---

## Project Layout (one possible layout)
- src/api/ (HTTP handlers)
- src/services/ (business logic: bids, auctions)
- src/db/ (migrations, queries)
- src/jobs/ (end auctions, notifications)
- src/realtime/ (SSE)
- src/lib/ (auth, rate limit, config)
- tests/

---

## Open Choices to Confirm
- Language/framework: Node (Express/Fastify), Python (FastAPI), Go (net/http), etc.
- SQL engine: Postgres/MySQL in prod; SQLite in dev.
- Real-time: SSE by default.
- Extension window default: 2–5 minutes.

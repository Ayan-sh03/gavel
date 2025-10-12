CREATE TABLE IF NOT EXISTS users (
    id TEXT PRIMARY KEY,
    email TEXT NOT NULL UNIQUE,
    password_hash TEXT NOT NULL,
    display_name TEXT NOT NULL,
    role TEXT NOT NULL DEFAULT 'user',
    status TEXT NOT NULL DEFAULT 'active',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS listings (
    id TEXT PRIMARY KEY,
    seller_id TEXT NOT NULL REFERENCES users(id),
    title TEXT NOT NULL,
    description TEXT NOT NULL,
    category TEXT NOT NULL,
    condition TEXT NOT NULL,
    cover_image_url TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS listings_seller_id_idx ON listings (seller_id);
CREATE INDEX IF NOT EXISTS listings_created_at_idx ON listings (created_at DESC);

CREATE TABLE IF NOT EXISTS listing_images (
    id TEXT PRIMARY KEY,
    listing_id TEXT NOT NULL REFERENCES listings(id) ON DELETE CASCADE,
    url TEXT NOT NULL,
    position INTEGER NOT NULL DEFAULT 0
);

CREATE INDEX IF NOT EXISTS listing_images_listing_position_idx
    ON listing_images (listing_id, position);

CREATE TABLE IF NOT EXISTS auctions (
    id TEXT PRIMARY KEY,
    listing_id TEXT NOT NULL REFERENCES listings(id) ON DELETE CASCADE,
    status TEXT NOT NULL,
    currency TEXT NOT NULL,
    starting_price_cents BIGINT NOT NULL,
    reserve_price_cents BIGINT,
    min_increment_cents BIGINT NOT NULL,
    starts_at TIMESTAMPTZ NOT NULL,
    ends_at TIMESTAMPTZ NOT NULL,
    extension_window_secs INTEGER NOT NULL DEFAULT 180,
    current_price_cents BIGINT,
    current_winner_id TEXT REFERENCES users(id),
    bid_count INTEGER NOT NULL DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS auctions_status_starts_idx ON auctions (status, starts_at);
CREATE INDEX IF NOT EXISTS auctions_status_ends_idx ON auctions (status, ends_at);
CREATE INDEX IF NOT EXISTS auctions_listing_id_idx ON auctions (listing_id);
CREATE INDEX IF NOT EXISTS auctions_current_winner_idx ON auctions (current_winner_id);

CREATE TABLE IF NOT EXISTS bids (
    id TEXT PRIMARY KEY,
    auction_id TEXT NOT NULL REFERENCES auctions(id) ON DELETE CASCADE,
    bidder_id TEXT NOT NULL REFERENCES users(id),
    amount_cents BIGINT NOT NULL,
    placed_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    client_key TEXT,
    accepted BOOLEAN NOT NULL DEFAULT FALSE,
    source_ip TEXT,
    UNIQUE (auction_id, client_key)
);

CREATE INDEX IF NOT EXISTS bids_auction_time_idx ON bids (auction_id, placed_at DESC);
CREATE INDEX IF NOT EXISTS bids_auction_amount_idx ON bids (auction_id, amount_cents DESC);

CREATE TABLE IF NOT EXISTS watchlists (
    user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    auction_id TEXT NOT NULL REFERENCES auctions(id) ON DELETE CASCADE,
    PRIMARY KEY (user_id, auction_id)
);

CREATE TABLE IF NOT EXISTS payments (
    id TEXT PRIMARY KEY,
    auction_id TEXT NOT NULL REFERENCES auctions(id) ON DELETE CASCADE,
    winner_id TEXT NOT NULL REFERENCES users(id),
    amount_cents BIGINT NOT NULL,
    status TEXT NOT NULL,
    provider TEXT NOT NULL,
    provider_ref TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS payouts (
    id TEXT PRIMARY KEY,
    seller_id TEXT NOT NULL REFERENCES users(id),
    auction_id TEXT NOT NULL REFERENCES auctions(id) ON DELETE CASCADE,
    amount_cents BIGINT NOT NULL,
    status TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS audit_log (
    id TEXT PRIMARY KEY,
    actor_id TEXT REFERENCES users(id),
    action TEXT NOT NULL,
    subject_type TEXT NOT NULL,
    subject_id TEXT NOT NULL,
    meta_json JSONB,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS audit_log_subject_idx
    ON audit_log (subject_type, subject_id, created_at);

CREATE TABLE IF NOT EXISTS users (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    login        VARCHAR(255) UNIQUE NOT NULL,
    password_hash VARCHAR(255) NOT NULL,
    balance      DECIMAL(18, 2) NOT NULL DEFAULT 0,
    withdrawn    DECIMAL(18, 2) NOT NULL DEFAULT 0,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS orders (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    number      VARCHAR(255) UNIQUE NOT NULL,
    user_id     UUID NOT NULL REFERENCES users(id),
    status      VARCHAR(20) NOT NULL DEFAULT 'NEW',
    accrual     DECIMAL(18, 2),
    uploaded_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS withdrawals (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id      UUID NOT NULL REFERENCES users(id),
    order_number VARCHAR(255) NOT NULL,
    sum          DECIMAL(18, 2) NOT NULL,
    processed_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

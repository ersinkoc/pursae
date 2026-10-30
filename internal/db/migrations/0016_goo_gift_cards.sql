-- Goocommerce Gift Cards module: issued gift cards and their balance history. A card is issued by a
-- paid order (order_id / order_item_id) or by hand; status is active, disabled or redeemed (fully
-- used). Activity types: issued, used (paid for an order), refunded (credited back), manual.
CREATE TABLE IF NOT EXISTS goo_gift_cards (
    id             bigserial PRIMARY KEY,
    code           text NOT NULL UNIQUE,
    initial        numeric(19,4) NOT NULL DEFAULT 0,
    balance        numeric(19,4) NOT NULL DEFAULT 0,
    currency       text NOT NULL DEFAULT '',
    order_id       bigint NOT NULL DEFAULT 0,
    order_item_id  bigint NOT NULL DEFAULT 0,
    recipient      text NOT NULL DEFAULT '',
    recipient_name text NOT NULL DEFAULT '',
    sender         text NOT NULL DEFAULT '',
    sender_email   text NOT NULL DEFAULT '',
    message        text NOT NULL DEFAULT '',
    deliver_date   date,
    delivered      boolean NOT NULL DEFAULT false,
    expires        date,
    status         text NOT NULL DEFAULT 'active',
    redeemed_by    bigint NOT NULL DEFAULT 0,
    created        timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS goo_gift_cards_recipient ON goo_gift_cards (lower(recipient));
CREATE INDEX IF NOT EXISTS goo_gift_cards_order_item ON goo_gift_cards (order_item_id);

CREATE TABLE IF NOT EXISTS goo_gift_card_activity (
    id        bigserial PRIMARY KEY,
    card_id   bigint NOT NULL REFERENCES goo_gift_cards(id) ON DELETE CASCADE,
    type      text NOT NULL,
    amount    numeric(19,4) NOT NULL DEFAULT 0,
    order_id  bigint NOT NULL DEFAULT 0,
    user_id   bigint NOT NULL DEFAULT 0,
    note      text NOT NULL DEFAULT '',
    date      timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS goo_gift_card_activity_card ON goo_gift_card_activity (card_id);
CREATE INDEX IF NOT EXISTS goo_gift_card_activity_order ON goo_gift_card_activity (order_id);

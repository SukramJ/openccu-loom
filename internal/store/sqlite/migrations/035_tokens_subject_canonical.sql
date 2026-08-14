-- Token subjects are canonicalised — trimmed and lower-cased — on write, the
-- same folding the users table has always applied. Rows written before that
-- carry whatever spelling the operator typed, which splits one account into
-- two identities: the bearer identity a token produces is compared verbatim by
-- the per-subject stores (preferences, private diagram ownership) and cannot be
-- addressed by any user-side operation that uses the stored spelling.
--
-- lower() folds ASCII only, which is exactly the fold the write path performs,
-- so both stay on the same rule.

-- +goose Up
UPDATE tokens SET subject = lower(trim(subject));

-- Down is a no-op: the operator's original spelling was not retained anywhere,
-- so the fold cannot be undone. Nothing is dropped and no token stops working —
-- a downgraded daemon reads the canonical subject as it would any other.
-- +goose Down
SELECT 1;

-- Token subjects are canonicalised — trimmed and lower-cased — on write, the
-- same folding the users table has always applied. Rows written before that
-- carry whatever spelling the operator typed, which splits one account into
-- two identities: the bearer identity a token produces is compared verbatim by
-- the per-subject stores (preferences, private diagram ownership) and cannot be
-- addressed by any user-side operation that uses the stored spelling.
--
-- The fold below is not identical to the write path's: SQLite's lower() and
-- trim() are ASCII-only, while the Go side uses strings.ToLower and
-- strings.TrimSpace, which are Unicode-aware. A legacy subject carrying a
-- non-ASCII upper-case letter (Emile with an acute É, a Cyrillic name, an
-- all-caps umlaut) or non-breaking whitespace therefore keeps a spelling no
-- Go-side lookup produces: its per-subject side-stores stay split, and the
-- purge behind an account deletion does not match it. Folding those rows
-- needs a Go pass, which this migration deliberately does not attempt — it
-- fixes the ASCII and plain-space cases and leaves the rest no worse than
-- before.

-- +goose Up
UPDATE tokens SET subject = lower(trim(subject));

-- Down is a no-op: the operator's original spelling was not retained anywhere,
-- so the fold cannot be undone. Nothing is dropped and no token stops working —
-- a downgraded daemon reads the canonical subject as it would any other.
-- +goose Down
SELECT 1;

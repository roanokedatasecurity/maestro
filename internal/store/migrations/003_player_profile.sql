-- Add optional profile column to players.
-- Profile is a JSON-encoded PlayerProfile struct; NULL for players created
-- without a profile (all existing rows). Column is nullable intentionally —
-- profile is always optional and existing callers are unaffected.
ALTER TABLE players ADD COLUMN profile TEXT;

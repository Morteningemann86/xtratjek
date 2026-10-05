-- Migration 013: split a meeting's hand-typed notes from its transcript.
--
-- Until now `transcript` did double duty for both typed/pasted text and the
-- machine-produced transcription, so a re-run of transcription could
-- silently overwrite something the user had written themselves. Existing
-- rows cannot be told apart after the fact (there is no record of which
-- parts were typed vs. transcribed), so this adds the new column empty
-- rather than guessing — nothing already in `transcript` is moved.
ALTER TABLE meetings ADD COLUMN notes TEXT NOT NULL DEFAULT '';

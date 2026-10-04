-- HUI-2598: a completed follow-up can mean "waiting on the customer"
-- without inventing a second lead status. Empty stays the old meaning.

ALTER TABLE follow_ups ADD COLUMN disposition TEXT NOT NULL DEFAULT '';

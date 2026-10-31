-- Technical addresses were used briefly as buyer identifiers. Clear them from
-- user profiles so the next purchase asks for a deliverable receipt address.
-- Subscription rows retain the address originally sent to Lava because that
-- exact value is required to cancel an existing contract.
UPDATE users
SET email = NULL,
    pending_flow = ''
WHERE email ~ '^tg_[0-9]+@tomas\.blank$';

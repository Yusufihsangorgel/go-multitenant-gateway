-- Notes table for the example notes module. Unqualified on purpose: the
-- runner pins search_path to one tenant schema per transaction, so this
-- lands in whichever tenant schema is being migrated.
CREATE TABLE IF NOT EXISTS notes (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    user_id text NOT NULL,
    text text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now()
);

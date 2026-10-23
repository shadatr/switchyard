-- +goose Up
CREATE TABLE projects (
  id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  name        text NOT NULL UNIQUE,
  created_at  timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE api_keys (
  id                 uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  project_id         uuid NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
  name               text NOT NULL,
  prefix             text NOT NULL,
  hash               bytea NOT NULL UNIQUE,
  allowed_models     text[] NOT NULL DEFAULT '{}',
  rpm_limit          integer CHECK (rpm_limit IS NULL OR rpm_limit > 0),
  tpm_limit          integer CHECK (tpm_limit IS NULL OR tpm_limit > 0),
  monthly_budget_usd numeric(12,4) CHECK (monthly_budget_usd IS NULL OR monthly_budget_usd >= 0),
  disabled           boolean NOT NULL DEFAULT false,
  created_at         timestamptz NOT NULL DEFAULT now(),
  last_used_at       timestamptz
);

CREATE INDEX api_keys_project_id_idx ON api_keys (project_id);

-- +goose Down
DROP TABLE api_keys;
DROP TABLE projects;

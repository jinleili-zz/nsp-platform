-- taskqueue schema for PostgreSQL

CREATE TABLE IF NOT EXISTS tq_workflows (
    id              VARCHAR(36) PRIMARY KEY,
    name            VARCHAR(255) NOT NULL,
    resource_type   VARCHAR(64)  NOT NULL,
    resource_id     VARCHAR(255) NOT NULL,
    status          VARCHAR(32)  NOT NULL DEFAULT 'pending',
    total_steps     INT          NOT NULL DEFAULT 0,
    completed_steps INT          NOT NULL DEFAULT 0,
    failed_steps    INT          NOT NULL DEFAULT 0,
    error_message   TEXT,
    metadata        JSONB,
    created_at      TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ  NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS tq_steps (
    id              VARCHAR(36)  PRIMARY KEY,
    workflow_id     VARCHAR(36)  NOT NULL REFERENCES tq_workflows(id),
    step_order      INT          NOT NULL,
    task_type       VARCHAR(128) NOT NULL,
    task_name       VARCHAR(255) NOT NULL,
    params          JSONB,
    status          VARCHAR(32)  NOT NULL DEFAULT 'pending',
    priority        INT          NOT NULL DEFAULT 3,
    queue_tag       VARCHAR(64),
    broker_task_id  VARCHAR(255),
    result          JSONB,
    error_message   TEXT,
    retry_count     INT          NOT NULL DEFAULT 0,
    max_retries     INT          NOT NULL DEFAULT 3,
    created_at      TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    queued_at       TIMESTAMPTZ,
    started_at      TIMESTAMPTZ,
    completed_at    TIMESTAMPTZ,
    updated_at      TIMESTAMPTZ  NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_tq_steps_workflow_order  ON tq_steps(workflow_id, step_order);
CREATE INDEX IF NOT EXISTS idx_tq_steps_workflow_status ON tq_steps(workflow_id, status);
CREATE INDEX IF NOT EXISTS idx_tq_workflows_resource    ON tq_workflows(resource_type, resource_id);

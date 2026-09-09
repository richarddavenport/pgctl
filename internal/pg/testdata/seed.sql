-- A schema for TestIntrospectAgainstRealServer to read.
--
-- The test's contract is "point me at a populated database and I will check the
-- catalog queries against reality", so an empty cluster fails it — which is
-- what CI did, because a `postgres:17` service container has no tables. This is
-- the smallest schema that exercises every assertion the test makes rather than
-- merely getting past the first one.
--
-- Deliberately included:
--   * two schemas, so a qualified name is not the same as a bare one;
--   * a foreign-key RING (cancellation <-> policy_contract), because the load
--     order's SCC condensation is only tested by a graph that has one, and both
--     real rings found in the estate this was built for were of exactly this
--     shape;
--   * a tail hanging off the ring, which is what makes a wrong ordering
--     expensive rather than merely wrong;
--   * a secondary index, so Indexes() has something to return;
--   * an hdb_catalog schema, because the test asks Introspect to EXCLUDE it and
--     an exclusion that excludes nothing proves nothing.

CREATE SCHEMA IF NOT EXISTS operations;
CREATE SCHEMA IF NOT EXISTS hdb_catalog;

CREATE TABLE operations.account (
	id   bigint PRIMARY KEY,
	name text NOT NULL
);

CREATE TABLE operations.policy_contract (
	id             bigint PRIMARY KEY,
	account_id     bigint NOT NULL REFERENCES operations.account (id),
	-- Half the ring. Added below as a constraint, because the table it points
	-- at does not exist yet.
	cancellation_id bigint
);

CREATE TABLE operations.cancellation (
	id                  bigint PRIMARY KEY,
	policy_contract_id  bigint NOT NULL REFERENCES operations.policy_contract (id)
);

ALTER TABLE operations.policy_contract
	ADD CONSTRAINT policy_contract_cancellation_fkey
	FOREIGN KEY (cancellation_id) REFERENCES operations.cancellation (id);

-- The tail: three tables deep, so the load order has layers to get wrong.
CREATE TABLE operations.policy_contract_quote (
	id                 bigint PRIMARY KEY,
	policy_contract_id bigint NOT NULL REFERENCES operations.policy_contract (id)
);

CREATE TABLE operations.quote_line (
	id       bigint PRIMARY KEY,
	quote_id bigint NOT NULL REFERENCES operations.policy_contract_quote (id),
	amount   numeric(12, 2) NOT NULL
);

CREATE INDEX quote_line_quote_id_idx ON operations.quote_line (quote_id);

-- Excluded by the test, and it must exist for that to mean anything.
CREATE TABLE hdb_catalog.event_log (
	id      bigint PRIMARY KEY,
	payload jsonb NOT NULL
);

INSERT INTO operations.account VALUES (1, 'first'), (2, 'second');
INSERT INTO operations.policy_contract VALUES (1, 1, NULL), (2, 2, NULL);
INSERT INTO operations.cancellation VALUES (1, 1);
UPDATE operations.policy_contract SET cancellation_id = 1 WHERE id = 1;
INSERT INTO operations.policy_contract_quote VALUES (1, 1), (2, 2);
INSERT INTO operations.quote_line VALUES (1, 1, 100.00), (2, 2, 250.50);
INSERT INTO hdb_catalog.event_log VALUES (1, '{"kind":"insert"}');

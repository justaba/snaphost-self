-- =============================================================================
-- Migration: 0013_drop_billing
-- Description: Removes the vibecoin ledger and every column that referenced it.
--              A self-hosted platform run by its own operator has nobody to
--              bill, so the wallet, the transaction ledger, and the saga's
--              reserve/commit/refund participation are not a disabled feature
--              — they are a concept the product no longer has.
--
--              The saga itself is untouched beyond these columns. Compensation
--              still tears down a runtime that was started before a later step
--              failed; only the refund left with the money.
-- Dependencies: 0001_wallets, 0002_transactions, 0003_deploys, 0005_deploy_saga
-- =============================================================================

-- ---------------------------------------------------------------------------
-- deploys: the cost and its reservation
-- ---------------------------------------------------------------------------

alter table deploys
    drop constraint if exists chk_deploys_cost_non_negative;

alter table deploys
    drop column if exists cost_vibecoins,
    drop column if exists reservation_tx_id;

-- ---------------------------------------------------------------------------
-- deploy_sagas: the two coin flags and the reservation pointer
--
-- 'reserved' also stops being a reachable step. The state machine now goes
-- pending → building directly, which makes 'pending' a step the saga can sit
-- in while an enqueue is retried — previously it was traversed instantly and
-- the resume sweeper did not look for it.
-- ---------------------------------------------------------------------------

alter table deploy_sagas
    drop column if exists coins_reserved,
    drop column if exists coins_committed,
    drop column if exists reservation_tx_id;

update deploy_sagas set current_step = 'pending' where current_step = 'reserved';
update deploys      set status       = 'pending' where status       = 'reserved';

-- ---------------------------------------------------------------------------
-- The ledger itself
--
-- transactions is dropped first: it references wallets, and dropping the
-- parent first would need a cascade that could take more with it than intended.
-- ---------------------------------------------------------------------------

drop table if exists transactions;
drop table if exists wallets;

BEGIN;

CREATE UNIQUE INDEX affiliate_commission_entries_earned_cycle_unique
    ON affiliate_commission_entries (provider_subscription_id,rule_version,cycle)
    WHERE kind='earned';

COMMIT;

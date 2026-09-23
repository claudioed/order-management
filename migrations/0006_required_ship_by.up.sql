-- ADR 0020 §2: an order may arrive with an externally-dictated deadline
-- (network-originated demand carries a required ship-by the retailer set,
-- not one we chose). When present, the promise is constrained to a window
-- at or before this instant via PromisePolicy.FeasibleBy, instead of
-- being the earliest window this service can manage.
--
-- NULLABLE and with no default, deliberately: the overwhelming majority
-- of orders have no external deadline, and "no deadline" must stay
-- distinguishable from "a deadline that happens to be the zero instant"
-- — the latter would make every ordinary order look infeasible.
--
-- Additive: every pre-existing row reads NULL and is promised exactly as
-- it was before this migration.
ALTER TABLE orders
    ADD COLUMN required_ship_by TIMESTAMPTZ;

-- Runtime compatibility boundary, despite no new table shape. Older controllers
-- do not understand kind=job and could give a migration an automatic restart
-- policy. Advancing the existing schema ledger makes their startup/downgrade
-- preflight reject this state before they can reconcile job specifications.
SELECT 1;

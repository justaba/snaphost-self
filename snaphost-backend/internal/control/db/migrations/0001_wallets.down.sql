drop view if exists wallet_totals;
drop trigger if exists wallets_updated_at on wallets;
drop function if exists public.handle_updated_at();
drop table if exists wallets;

-- =============================================================================
-- Migration: 0011_custom_domains (down)
-- =============================================================================

drop trigger if exists custom_domains_updated_at on custom_domains;
drop table if exists custom_domains;

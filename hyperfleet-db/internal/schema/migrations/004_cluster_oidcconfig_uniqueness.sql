-- OidcConfig:Cluster is a 1:1 relationship -- each OidcConfig backs at most
-- one cluster (HCP). platform-api's application-level check (see
-- resolveOidcConfig in cluster.go) is a List-then-Create and is not atomic,
-- so two concurrent creates referencing the same oidcConfigId could both
-- pass the check and both succeed. Enforce uniqueness here as the atomic
-- safety net (same pattern as idx_oidcconfig_unmanaged_issuer_url in
-- 003_oidcconfig_issuer_url_uniqueness.sql).
--
-- Cluster rows are namespaced by clusterID (not by account), so unlike that
-- migration this scopes by the hyperfleet.io/account-id label stored in
-- metadata rather than by the namespace column.
CREATE UNIQUE INDEX IF NOT EXISTS idx_cluster_oidcconfig_id
    ON kubernetes_resources ((metadata->'labels'->>'hyperfleet.io/account-id'), (spec->>'oidcConfigId'))
    WHERE gvk = 'hyperfleet.io/v1alpha1/Cluster'
      AND deletion_timestamp IS NULL
      AND spec->>'oidcConfigId' IS NOT NULL
      AND spec->>'oidcConfigId' != '';

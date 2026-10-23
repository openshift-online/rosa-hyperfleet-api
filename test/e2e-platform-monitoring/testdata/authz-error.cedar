// Ephemeral test only: bind to one approved principal and one existing resource UID.
forbid(
    principal,
    action == Seaplane::Action::"DescribeCluster",
    resource == Seaplane::Cluster::"@@ACCOUNT_ID@@/@@REGION@@/@@RESOURCE_UID@@"
) when {
    resource.hasTag("example.com/fault") &&
    resource.getTag("example.com/fault") == "@@RUN_ID@@" &&
    9223372036854775807 + 1 == 0
};

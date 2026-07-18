# crane-cost-collector

This chart installs only the read-only multi-cloud cost collector. It does not
install crane-agent, EHPA, eviction, Metric Adapter, or cluster-wide RBAC.

The chart intentionally requires one replica and persistent storage because the
current ledger backend is a crash-safe local file. Create the Secret named by
`credentials.existingSecret` using your normal secret-management system; this
chart never accepts or renders credential values. The same Secret must contain
`api_token`; `/api/v1/*` requires it as a Bearer token and rereads it on every
request so it can rotate without restarting the pod.

Install into a dedicated namespace. The pod conforms to the Kubernetes
Restricted Pod Security Standard, so the namespace can safely enforce that
profile.

The supported Kubernetes installation matrix is 1.34, 1.35, and 1.36. Replace
all account placeholders in `config.providers`, select only the provider entries
and Secret item mappings you use, and set `image.digest` to the published
`sha256:...` digest before production use.

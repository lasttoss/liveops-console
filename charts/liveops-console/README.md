# liveops-console chart

Two Deployments of one image (`--mode server` and `--mode worker`), a Service, a Secret,
and a NetworkPolicy that is on by default.

| value | default | why |
|---|---|---|
| `replicaCount` | `1` | the read path is one indexed row lookup; replicas are for availability, not for throughput |
| `worker.replicaCount` | `2` | safe by construction: the claim is one `UPDATE ... FOR UPDATE SKIP LOCKED`, so two pods cannot produce the same effect twice |
| `worker.tick` | `1s` | how often the worker looks for due fires; the claim is what makes a frequent look harmless |
| `migrateOnStart` | `true` | guarded by an advisory lock, so two replicas cannot race. Set false when the migration should be its own reviewable step |
| `autoscaling.enabled` | `false` | writes are serialised per key, so more pods help reads and do nothing for writes. Off until a load test says otherwise |
| `networkPolicy.enabled` | `true` | `/metrics` and `/healthz` answer without a token, so the network *is* the access decision. Off is the surprising choice here |
| `networkPolicy.ingress.panel` | `true` | the panel of the same release reaches the API; nothing else does |
| `networkPolicy.ingress.metricsNamespaceLabel` | empty | a monitoring namespace opts in by carrying this label. Empty means nobody scrapes, which is a decision rather than an oversight |
| `networkPolicy.kubeletCIDR` | empty | on Calico and Cilium an ingress rule can refuse kubelet probe traffic, after which the pod is restarted for being alive. Set your node CIDR, or leave the probes failing for a reason nobody enjoys debugging |
| `auth.existingSecret` | empty | bring your own secret; otherwise one is created from the values |
| `auth.productionEnvs` | `prod,production` | writes in these environments need the `admin` role; reads do not |

**No PodDisruptionBudget.** A PDB for a Deployment whose writes are already serialised
would mostly block node drains, and the worker's guarantee does not depend on pods
staying up: a restart re-claims nothing, because the row it would claim is no longer
pending.

**The worker has no probes.** Its liveness is "the loop is still reaching the database";
a failed query stops the process and the restart is the retry. That is only safe because
the effect cannot happen twice - which is the property the whole repository is about.

# Kubernetes reference topology

These manifests encode Phase 2 workload and security defaults for review. They
are intentionally not a deployable environment yet: release automation must
replace `registry.invalid/...:release-placeholder`, inject managed secret
references, and supply the app-api and worker process modes before promotion.

The reference proves the intended unit of scaling: shared workload classes in
a cell. Nothing here creates a Deployment, Service, namespace, database, or
credential per Spyglass Account.

Before an environment overlay may use these resources it must add:

- A pinned image digest produced by the verified release workflow.
- External Secrets or workload identity; never literal Secret values.
- Cell-specific database, object-store, Temporal, and queue references.
- Ingress/WAF, certificate, DNS, and trusted route-signing configuration.
- HPA custom metrics for request latency, queue age, and schedule-to-start.
- Tested NetworkPolicy egress destinations and cluster admission policy.
- Pod monitor, alerts, SLO metadata, and a load-tested replica/connection cap.

Run `kubectl kustomize deploy/kubernetes/reference` as a structural render
check. Do not apply the output to a cluster.

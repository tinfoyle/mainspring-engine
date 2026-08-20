# Linode LKE Environments

The repository now has first-pass `linode-preproduction` and `linode-production` Kustomize renders. Both include the full Spyglass reference topology, website, ingress/cert-manager objects and three CloudNativePG clusters. Application, website and dynamic runner images are digest-shaped; the all-zero digest is a deliberate non-deployable placeholder replaced by release promotion.

Run the credential-free structural gate from `ubunturojo`:

```bash
bash deploy/kubernetes/overlays/verify.sh
```

These renders are not apply-ready yet. The kubeconfig inventory must confirm Kubernetes/LKE versions, CNI NetworkPolicy behavior, storage classes, zones/node pools, ingress-nginx, cert-manager, metrics-server, CloudNativePG, secret encryption/controller choice and an actual sandbox RuntimeClass. Pre-production and production now use disjoint application/runner namespace pairs and disjoint cluster-scoped runner TokenReview RBAC names, whether they ultimately share a cluster or use separate clusters. All internal DNS names, RBAC subjects and cross-namespace NetworkPolicy selectors are rewritten to the environment boundary, and the verifier rejects any retained `spyglass-reference` name. The placeholder storage class `spyglass-lke-block-storage` must still be mapped to a verified LKE class rather than guessed.

No Secret payload is committed. Environment configuration must create the workload-specific ConfigMaps/Secrets already named by the reference, preferably from SOPS/age-encrypted inputs with the age key outside Git. Database backup configuration remains an explicit CloudNativePG extension point for the owner after the clusters exist.

# Kubernetes deployment

Lore is published as `ghcr.io/gregbuehler/lore`; its Helm chart is published at `oci://ghcr.io/gregbuehler/charts/lore`. The source chart is `helm/lore`, and `deploy/argocd/lore.yaml` defines the ArgoCD application. The chart fixes the Deployment at one replica with `Recreate`: Lore's SQLite index is a single-writer database. Both the Markdown vault and the derived `.lore/index-data` SQLite index are stored on the same PVC. Do not run concurrent Lore pods against the same vault.

The image runs as UID/GID 10001. The API binds to the pod interface on port 7520, but Kubernetes exposes it only through a `ClusterIP` Service. API routes require `LORE_API_TOKEN`; `/healthz` and `/readyz` are intentionally public probe routes and do not return vault content. Kubernetes uses these HTTP routes for liveness and readiness. Store the token outside chart values in a Kubernetes Secret. For example, create the namespace and secret interactively without putting a production token in shell history:

```sh
kubectl create namespace lore
read -rsp 'Lore API token: ' LORE_API_TOKEN; echo
kubectl -n lore create secret generic lore-api --from-literal=LORE_API_TOKEN="$LORE_API_TOKEN"
unset LORE_API_TOKEN
```

For a private GHCR package, provision a pull secret named `ghcr-pull-secret` in namespace `lore` with registry credentials permitted to read the image. Never put the credential or API token in Helm values, Git, container image layers, or ArgoCD manifests.

## Initialize an empty vault or import a chosen vault

No personal vault is cloned or seeded automatically. The PVC starts empty. Initialize it from a trusted Lore CLI environment that can access the cluster's pod volume. One option is to temporarily port-forward the deployment's PVC via a maintenance pod, or use the cluster's approved storage workflow; stop the Lore Deployment while modifying the mounted vault. Initialize an empty directory with `lore vault init <path>` using the CLI version appropriate to the selected release, then copy the resulting Markdown files and `.lore/config.yaml` into the PVC's `/vault` root. `lore daemon start --vault /vault` requires the vault's `.lore/config.yaml` to exist. Keep the configuration file free of credentials and review it before use.

To import a selected Markdown/Git vault, explicitly select the source, review its contents and `.lore/config.yaml`, then copy or restore that vault's contents into `/vault` using your approved transfer method. Do not configure an automatic clone or point deployment tooling at a user's existing local vault. Lore watches and indexes Markdown content in place; its index is regenerated/maintained in `.lore/index-data` on the PVC.

If a separately managed ConfigMap must provide the vault config, set `config.existingConfigMap` to its name. It must contain a `config.yaml` key and is mounted read-only at `/vault/.lore/config.yaml`; the ConfigMap must not contain secrets. By default the chart does not create or mount a config file. Keep `.lore/config.yaml` on the persistent volume for normal operation so user configuration is retained with the vault.

## Deploy

Direct Helm installation:

```sh
helm upgrade --install lore oci://ghcr.io/gregbuehler/charts/lore \
  --namespace lore --create-namespace \
  --values helm/lore/examples/home-cluster.yaml
```

Alternatively apply `deploy/argocd/lore.yaml`. ArgoCD follows version tags and supplies its resolved revision as the immutable image revision. Ensure the token Secret and GHCR pull Secret exist before syncing.

Public ingress is disabled by default. The optional home-cluster example enables `lore.vectaryon.com` ingress with nginx, TLS from cert-manager's `letsencrypt-production` ClusterIssuer, and an editable source-IP allow-list. This ingress is still authenticated by the API token and should only be enabled where the network boundary is trusted. For cluster-internal Grimoire access, use the Service `lore.lore.svc.cluster.local:7520` and leave ingress disabled if no external client needs it.

## Backups and recovery

Back up the complete PVC, including the Markdown vault, `.lore/config.yaml`, `.lore/index-data`, and any other intentionally retained Lore state. Prefer a CSI/storage snapshot or a quiesced filesystem copy; stop the Deployment before a file-level copy of SQLite/WAL data, then restart it after the backup. Protect backups as private user data and encrypt them at rest and in transit. Test recovery by restoring into a separate PVC and checking the vault before switching service back. The derived SQLite index can be rebuilt from Markdown by restarting Lore if necessary, but the source Markdown and configuration must be backed up.

Rotate the API token by updating the Kubernetes Secret, then restart the Deployment so the process receives the new environment value. Keep tokens out of command transcripts when possible; the illustrative `kubectl create secret --from-literal` command passes its value in the client process arguments.

## API and implementation assumptions

The deployment invokes `lore daemon start --vault /vault`; that is the foreground daemon command, not a separate `lore serve` command. It sets `LORE_API_ADDR=0.0.0.0:7520`, `LORE_API_TOKEN` from the referenced Secret, and `LORE_DB=/vault/.lore/index-data` as the base for the per-vault derived SQLite index under `.lore/index-data/vaults/<vault-id>/index.db`, keeping it durable on the PVC. HTTP starts alongside the daemon only when the token is configured; the listener defaults to loopback unless explicitly set. The no-content `/healthz` and `/readyz` routes are public only for Kubernetes probes; all API endpoints remain token-authenticated. The chart assumes the API listens on the configured fixed container port 7520 and that `.lore/config.yaml` is supplied as a mounted volume or stored in the vault, not generated from default chart content.

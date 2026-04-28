# ado-token

Kubernetes helper that authenticates to Azure DevOps using a service principal,
obtains a bearer token, and keeps a Kubernetes secret up to date with it.

Intended as a companion to [ArgoCD Image Updater](https://argocd-image-updater.readthedocs.io/)
when checking Azure Container Registry for new image tags and updating Git with them.

## How it works

On startup and then on a recurring schedule, the helper:

1. Reads service principal credentials from a Kubernetes secret
2. Exchanges them for a bearer token via the Azure AD OAuth2 client credentials flow
3. Writes the token into a target Kubernetes secret

The refresh interval is derived from the token's `expires_in` field (refreshed at 80% of
TTL). An optional `REFRESH_INTERVAL` cap can be set to refresh more frequently.

## Deployment

### Prerequisites

- Kubernetes cluster
- Service principal with access to Azure DevOps
- A secret containing the service principal credentials (creation is out of scope —
  provide it via `kubectl`, Sealed Secrets, External Secrets Operator, or any other
  mechanism). Expected keys:

```yaml
tenant_id: <tenant-id>
client_id: <client-id>
client_secret: <client-secret>
```

  Example using kubectl:

```bash
kubectl create secret generic ado-credentials \
  --namespace argocd \
  --from-literal=tenant_id=<tenant-id> \
  --from-literal=client_id=<client-id> \
  --from-literal=client_secret=<client-secret>
```

### Helm (recommended)

```bash
helm install ado-token ./charts/ado-token \
  --namespace argocd \
  --set credentialsSecret.name=ado-credentials \
  --set outputSecret.name=ado-token \
  --set image.tag=v1.0.0
```

#### ArgoCD

Use `deploy/argocd-application.yaml` as a starting point, adjusting the values inline
or via a separate `values.yaml` per cluster:

```bash
kubectl apply -f deploy/argocd-application.yaml
```

#### Key chart values

| Value | Default | Description |
| --- | --- | --- |
| `image.repository` | `ghcr.io/krisiasty/ado-token` | Image repository |
| `image.tag` | Chart `appVersion` | Image tag |
| `credentialsSecret.name` | `ado-credentials` | Secret with service principal credentials |
| `credentialsSecret.namespace` | Release namespace | Override if credentials live elsewhere |
| `outputSecret.name` | `ado-token` | Secret to write the bearer token into |
| `outputSecret.namespace` | Release namespace | Override if output secret lives elsewhere |
| `outputSecret.key` | `token` | Key within the output secret |
| `refreshInterval` | _(unset)_ | Cap on refresh interval, e.g. `30m` |
| `health.port` | `8080` | Port for `/livez` and `/readyz` |

### Plain manifests

```bash
# RBAC (ServiceAccount, Role, RoleBinding)
kubectl apply -f deploy/rbac.yaml

# Pre-create the output secret (required before the helper starts)
kubectl apply -f deploy/secret.yaml

# Deployment
kubectl apply -f deploy/deployment.yaml
```

The default manifests deploy into the `argocd` namespace. Edit the `namespace` fields
in all three files if you need a different namespace, and update the `resourceNames` in
`deploy/rbac.yaml` if you use different secret names.

### Configuration

All configuration is provided via environment variables:

| Variable | Required | Default | Description |
| --- | --- | --- | --- |
| `CREDENTIALS_SECRET_NAME` | yes | | Name of the secret containing service principal credentials |
| `CREDENTIALS_SECRET_NAMESPACE` | yes | | Namespace of the credentials secret |
| `OUTPUT_SECRET_NAME` | yes | | Name of the secret to write the token into |
| `OUTPUT_SECRET_NAMESPACE` | yes | | Namespace of the output secret |
| `OUTPUT_SECRET_KEY` | no | `token` | Key within the output secret |
| `REFRESH_INTERVAL` | no | | Cap on refresh interval, e.g. `30m`. Defaults to 80% of token TTL |
| `HEALTH_PORT` | no | `8080` | Port for `/livez` and `/readyz` health endpoints |

### Health probes

| Endpoint | Purpose |
| --- | --- |
| `/livez` | Liveness — fails if the refresh loop has stalled |
| `/readyz` | Readiness — passes only after the first successful token write |

## Usage with ArgoCD Image Updater

Reference the output secret in your ArgoCD Application annotations:

```yaml
argocd-image-updater.argoproj.io/image-list: myimage=your-registry/your-image
argocd-image-updater.argoproj.io/myimage.pull-secret: secret:argocd/ado-token#token
```

ArgoCD Image Updater reads the secret via the Kubernetes API on each poll cycle, so
token rotation is picked up automatically without restarting any pods.

## RBAC notes

The helper's Role uses `resourceNames` to restrict access to exactly the two named
secrets. Because `resourceNames` is incompatible with the `create` verb in Kubernetes
RBAC, the output secret must be pre-created before the helper starts (see
`deploy/secret.yaml`).

## Building

```bash
# Local image build
docker build -t ado-token:latest .

# Release build (triggered automatically on version tag push)
git tag v1.0.0
git push origin v1.0.0
```

Releases are published to `ghcr.io/krisiasty/ado-token` via GitHub Actions using
GoReleaser. Multi-arch images (`linux/amd64`, `linux/arm64`) are built and published
as a combined manifest.

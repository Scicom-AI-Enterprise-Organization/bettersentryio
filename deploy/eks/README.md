# Deploying to aies-eks

Conventions taken from the live repos, not invented: `infra-dev/apps/enterprise-template`
(ingress class, cert-manager issuer, reloader annotation, backstage label, ECR path),
`infra-dev/sealed-secrets-public.pem` (offline sealing),
`infrastructure/terraform/environments/dev/shared` (ECR definitions),
`infra-foundation` (CNPG, the house Postgres).

Note this is **ingress-nginx + cert-manager**, not ALB. The AWS Load Balancer Controller is
installed on that cluster, but every app in `infra-dev` uses `ingressClassName: nginx` with
`cert-manager.io/cluster-issuer: aies-scicom-dev`, so this does too.

## Files

| File | Goes in | What it is |
|---|---|---|
| `00-terraform-ecr.md` | `infrastructure` | the `ecr_definitions` entry, and the one value I could not read |
| `deployment.yaml` | `infra-dev/apps/bettersentryio/` | engine + web Deployments and Services |
| `ingress.yaml` | same | two hosts — console and ingest |
| `secrets/bettersentryio.sealed.yaml` | same | SealedSecret, already sealed against their cert |
| `kustomization.yaml`, `imageupdater.yaml` | same | Kustomize + ArgoCD Image Updater |
| `argocd-app.yaml` | `infra-dev/app-of-apps/templates/bettersentryio.yaml` | registers the app |
| `postgres-helm-app.yaml` | `infra-dev/app-of-apps/templates/` | Postgres via Helm, in-cluster |
| `postgres-cnpg.yaml` | alternative | the same database as a CNPG Cluster |

## Two hosts, deliberately

- `bettersentryio.aies.scicom.dev` → the console (web)
- `bsio-ingest.aies.scicom.dev` → **only** `/api/0/beat`, `/api/0/errors`, `/clients`

Everything else on the engine — `/api/0/overview`, `/issues`, `/apps`, the admin endpoints —
is reachable in-cluster only. The UI calls it over the Service
(`http://bettersentryio-engine.bettersentryio.svc:9090`), which is what keeps the operator
token off the public path. If every monitored service runs inside this cluster, drop the
ingest Ingress entirely and the engine needs no public route at all.

## Order

1. **ECR** — `00-terraform-ecr.md`, then plan and apply. Nothing can be pushed before this.
2. **Postgres** — one of the two Postgres files. Wait for it to be ready; the engine
   retries a missing database on startup rather than crash-looping, but there is no point
   watching it retry.
3. **Images** — build and push both, then the app can sync:
   ```bash
   aws ecr get-login-password --region ap-southeast-5 \
     | docker login --username AWS --password-stdin 865626945255.dkr.ecr.ap-southeast-5.amazonaws.com
   R=865626945255.dkr.ecr.ap-southeast-5.amazonaws.com/scicom
   docker build --platform linux/amd64 -t $R/bettersentryio:v0.1.0 --build-arg VERSION=v0.1.0 .
   docker build --platform linux/amd64 -t $R/bettersentryio-web:v0.1.0 -f Dockerfile.web \
     --build-arg APP_VERSION=v0.1.0 .
   docker push $R/bettersentryio:v0.1.0
   docker push $R/bettersentryio-web:v0.1.0
   ```
   `--platform linux/amd64` matters: this laptop is arm64 and the nodes are not.
4. **App** — copy the manifests into `infra-dev`, commit, let ArgoCD sync.
5. **Seed the admin user**, once, against the fresh database:
   ```bash
   kubectl -n bettersentryio exec deploy/bettersentryio-web -- \
     sh -c 'npx prisma db push --skip-generate && npx tsx prisma/seed.ts'
   ```
   Prisma is pinned to `?schema=auth` in `DATABASE_URL`. **Never point it at `public`** —
   `db push` will offer to drop every Go-owned table, including `monitors` and `events`.

## The credentials, and a warning

The SealedSecret carries `admin@scicom.com.my` / `12345`, as asked. Both hosts are
internet-reachable over TLS, so that is a login anyone can find and guess. Two cheap ways to
close it without changing the app:

- **nginx basic auth at the ingress** — the same pattern `btw-web` already uses in this repo
  (`nginx.ingress.kubernetes.io/auth-type: basic`).
- **Entra ID** — the template already supports it; `web/src/lib/auth.ts` has the provider,
  it needs the client id/secret in the secret and the provider enabled.

The engine keeps its loud startup WARN either way, and the login page keeps its notice.

## What is not here

- **No CI workflow.** Image Updater watches ECR for semver tags, so something must build and
  push them. The `AIES-Bettersentryio-ECRAccessRole` in the Terraform entry exists for a
  GitHub Actions workflow that does not exist yet — the OIDC trust is what makes it
  possible, not sufficient.
- **No alert channel.** `--alert-webhook` is unset, so incidents open and resolve silently.
  Point it at a Teams webhook (stored in the same secret) to get notified.
- **replicas: 1 for the engine.** Safe to raise only once the detector takes its advisory
  lock (`store.LockDetector` is reserved for exactly this, ARCHITECTURE §2); two detectors
  without it means duplicate incidents.

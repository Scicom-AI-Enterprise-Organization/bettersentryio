# ECR — the change to make in `infrastructure`

Two repos: the engine and the UI are separate images (PLAN D11a — two processes).

Add one entry to `ecr_definitions` in
`infrastructure/terraform/environments/dev/shared/terraform.tfvars`, modelled exactly on
the `scicom/normielangfuse` entry already there — same GitHub org, so it needs the same
immutable-OIDC-subject treatment:

```hcl
{
  ecr_names     = ["scicom/bettersentryio", "scicom/bettersentryio-web"]
  ecr_role_name = "AIES-Bettersentryio-ECRAccessRole"
  # Scicom-AI-Enterprise-Organization has immutable OIDC subject claims enabled, so GitHub
  # presents sub=repo:<org>@<orgId>/<repo>@<repoId>:ref:... — same as normielangfuse above.
  # orgId 232325424 confirmed via `gh api orgs/Scicom-AI-Enterprise-Organization --jq .id`.
  github_repos = ["https://github.com/Scicom-AI-Enterprise-Organization@232325424/bettersentryio@REPO_ID"]
}
```

`REPO_ID` is the one value I could not read — the org's repos are SSO-gated and both local
`gh` accounts get 404 on them (`normielangfuse` too, though it is in the tfvars). Get it with
an authorized session:

    gh api repos/Scicom-AI-Enterprise-Organization/bettersentryio --jq .id

It cannot be left blank or omitted: `ecr/policies.tf` builds
`StringLike { "…:sub" = each.value.github_repo_patterns }`, and AWS rejects an empty
condition list with MalformedPolicyDocument — so `github_repos = []` fails at apply.

Then, from `terraform/environments/dev/shared`:

    aws sso login --sso-session default     # the cached refresh token is expired
    terraform init
    terraform plan -out=bsio.tfplan
    terraform apply bsio.tfplan

## Read the plan before applying

This is a **shared root module** — 68 ECR definitions plus VPC, CloudTrail and VPN
resources, on shared state at `s3://ai-es-iac/dev/shared`. The plan must contain only:

- 2 × `aws_ecr_repository` (scicom/bettersentryio, scicom/bettersentryio-web)
- 1 × `aws_iam_role` + its policies (AIES-Bettersentryio-ECRAccessRole)
- the repository policies for those two repos

Anything else in the diff is somebody else's drift, and applying would push it too. Stop
and ask if you see it.

Registry, confirmed from the live account: `865626945255.dkr.ecr.ap-southeast-5.amazonaws.com`,
repos named `scicom/<app>`, tags MUTABLE.

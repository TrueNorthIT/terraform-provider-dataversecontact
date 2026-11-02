---
page_title: "Signing in"
subcategory: ""
description: |-
  Sign in to the API's admin endpoints with the admin connection key, your Azure CLI login, or a pipeline identity that stores no secret.
---

# Signing in

The provider manages a scope through the API's admin endpoints, and it can sign
in to them in two ways:

| | How it works | Good for |
|---|---|---|
| Admin connection key | Sends the API's `ADMIN_CONNECTION_KEY` as the Bearer token | Existing configurations, and anywhere you already keep a secret |
| Microsoft Entra ID | Signs in as you (Azure CLI) or as an app (a pipeline or managed identity) | No shared secret: tokens last about an hour, and every change is made as a named person or app |

When a connection key is set, the provider uses it and ignores everything else
on this page, exactly as it always has. Without one it signs in with Microsoft
Entra ID, trying these in order:

1. `use_oidc`: a pipeline's workload identity federation (Azure DevOps or GitHub Actions).
2. `use_msi`: the managed identity of the machine running Terraform.
3. `use_cli` (on by default): your `az login`.

Entra sign-in needs provider version 1.8.0 or later.

## The admin connection key

Nothing has changed here. Set `DATAVERSE_CONTACT_CONNECTION_KEY` to the value of
`ADMIN_CONNECTION_KEY` on the API deployment:

```shell
export DATAVERSE_CONTACT_CONNECTION_KEY="<the API's ADMIN_CONNECTION_KEY>"
terraform plan
```

```terraform
provider "dataversecontact" {
  api_url = "https://api.dataverse-contact.tnapps.co.uk"
  # connection_key comes from DATAVERSE_CONTACT_CONNECTION_KEY
}
```

The key must match byte for byte. It is full admin on every scope of that
deployment and skips the API's Dataverse role check, so keep it in a secret
store (a pipeline secret variable, a variable group linked to Key Vault) and
never in configuration. The `connection_key` argument and the deprecated
`api_key` still work too.

## Signing in with Microsoft Entra ID

### What the API checks

For every scope, the API accepts a token only when:

- it was issued by the tenant that scope's Dataverse is in,
- it is for that Dataverse (its URL is the token's audience), and
- the person or app it names is a user in that Dataverse with System
  Customizer or System Administrator.

You don't configure the tenant or the Dataverse. The provider asks the API
(`GET /api/v2/_admin/scopes`) where each scope signs in and gets each token from
the right place, in another tenant too. A scope the API doesn't list yet,
because this apply is about to create it, signs in the same way as `default`.

System Customizer covers everything the provider does. For a person that means
a Dataverse user with the role. For a pipeline or managed identity it means an
application user: in the Power Platform admin centre, open the environment,
then *Settings > Users + permissions > Application users > New app user*, pick
the app registration and give it System Customizer. Do this in every Dataverse
environment the configuration manages.

If the API answers `403 Not authorized to administer scope`, the sign-in worked
and the role is missing. A `401` means the token came from the wrong tenant or
for the wrong Dataverse.

### From your own machine: the Azure CLI

Sign in with the Azure CLI, make sure no connection key is set, and run
Terraform:

```shell
az login
unset DATAVERSE_CONTACT_CONNECTION_KEY   # PowerShell: Remove-Item Env:DATAVERSE_CONTACT_CONNECTION_KEY
terraform plan
```

On Windows `az login` shows the Windows account picker; elsewhere it opens a
browser. The provider asks `az` for a token for each scope's Dataverse, in that
scope's tenant, once per run.

If a scope's Dataverse is in a tenant you haven't signed in to, sign in to it
once with `az login --tenant <tenant id>`. A guest account works as long as it
is a Dataverse user in that environment.

Set `use_cli = false` (or `ARM_USE_CLI=false`) if the provider should never use
your `az` login.

### Azure DevOps pipelines

Azure DevOps gives each job an OIDC token for a service connection, and Entra
exchanges it for an access token. This is workload identity federation:
nothing secret is stored in the pipeline, and nothing expires that someone has
to renew.

1. Create the service connection. In *Project settings > Service
   connections*, add an *Azure Resource Manager* connection that uses workload
   identity federation. If the pipeline already has one (for azurerm or the
   state backend) you can use it. For a connection only for this provider,
   the manual option lets you use an app registration that has no Azure role
   at all: create the app registration, start the connection, and add the
   issuer and subject the connection shows as a federated credential on the app
   registration.
2. Make it an application user with System Customizer in each Dataverse
   environment, as above.
3. Note two IDs: the app registration's application (client) ID, and the
   service connection's ID (the `resourceId` in its URL when you open it).
4. Run Terraform in a task that names the connection, with these variables:

```yaml
variables:
  dataverseClientId: "<app registration's client id>"
  dataverseConnectionId: "<service connection id>"

steps:
  - task: AzureCLI@2
    displayName: terraform apply
    inputs:
      azureSubscription: dataverse-contact-terraform   # the service connection's name
      scriptType: bash
      scriptLocation: inlineScript
      inlineScript: |
        terraform init
        terraform apply -auto-approve
    env:
      DATAVERSE_CONTACT_API_URL: https://api.dataverse-contact.tnapps.co.uk
      ARM_USE_OIDC: "true"
      ARM_CLIENT_ID: $(dataverseClientId)
      ARM_ADO_PIPELINE_SERVICE_CONNECTION_ID: $(dataverseConnectionId)
      SYSTEM_ACCESSTOKEN: $(System.AccessToken)
```

Two parts of this are easy to miss:

- `SYSTEM_ACCESSTOKEN: $(System.AccessToken)` must be in the step's `env`.
  Azure Pipelines never passes the job's access token to a step that doesn't ask
  for it, and the provider needs it to ask for the OIDC token.
- The connection must be named by a task in the job (`azureSubscription`
  above). Azure DevOps only hands out a connection's token to a job that uses
  it, and that is also when the connection's approvals and branch checks run.
  `TerraformTask` works too if your organisation has that extension.

These are the variables azurerm reads, so a pipeline that already signs azurerm
in with OIDC only needs the application user added. If the pipeline also sets
`DATAVERSE_CONTACT_CONNECTION_KEY`, the key wins.

### GitHub Actions

Add a federated credential to the app registration (*Certificates & secrets >
Federated credentials > Add > GitHub Actions deploying Azure resources*) for the
repository and branch or environment, make the app registration an application
user, and let the job request an ID token:

```yaml
permissions:
  id-token: write
  contents: read

jobs:
  apply:
    runs-on: ubuntu-latest
    env:
      DATAVERSE_CONTACT_API_URL: https://api.dataverse-contact.tnapps.co.uk
      ARM_USE_OIDC: "true"
      ARM_CLIENT_ID: ${{ vars.DATAVERSE_CLIENT_ID }}
    steps:
      - uses: actions/checkout@v4
      - uses: hashicorp/setup-terraform@v3
      - run: terraform init
      - run: terraform apply -auto-approve
```

### A managed identity

On a self-hosted agent that runs in Azure (a VM, a container), set
`ARM_USE_MSI=true`, plus `ARM_CLIENT_ID` for a user-assigned identity, and make
the identity an application user. A managed identity can only sign in to its own
tenant, so every Dataverse it manages must be in that tenant.

### Dataverse in another tenant

The token has to come from the tenant the scope's Dataverse is in, so the
identity has to exist there:

- Your own account: sign in to that tenant with `az login --tenant`. A guest
  account is fine.
- A pipeline: use an app registration in the customer's tenant (the customer
  creates it, adds the federated credential your service connection shows, and
  makes it an application user), with a service connection whose tenant is the
  customer's. One service connection covers one tenant. To manage several
  tenants from one configuration, give each its own provider block with an
  `alias`, `client_id` and `ado_pipeline_service_connection_id`.

## Settings

Each setting can also come from an environment variable. These are the names
azurerm and azuread use.

| Setting | Environment variable | What it does |
|---|---|---|
| `connection_key` | `DATAVERSE_CONTACT_CONNECTION_KEY` | The admin connection key. When set, nothing else on this page applies |
| `use_oidc` | `ARM_USE_OIDC` | Sign in through a pipeline's workload identity federation |
| `client_id` | `ARM_CLIENT_ID` | The app registration (or user-assigned managed identity) to sign in as |
| `ado_pipeline_service_connection_id` | `ARM_ADO_PIPELINE_SERVICE_CONNECTION_ID`, `ARM_OIDC_AZURE_SERVICE_CONNECTION_ID` | Azure DevOps: the service connection. Without it, `use_oidc` uses GitHub Actions |
| `use_msi` | `ARM_USE_MSI` | Sign in with the machine's managed identity |
| `use_cli` | `ARM_USE_CLI` | Sign in with `az login`. On by default |
| `tenant_id` | `ARM_TENANT_ID` | Only used when the API doesn't say which tenant a scope signs in to |

The pipeline supplies the rest: `SYSTEM_ACCESSTOKEN` (map it, as above) and
`SYSTEM_OIDCREQUESTURI` in Azure DevOps, `ACTIONS_ID_TOKEN_REQUEST_URL` and
`ACTIONS_ID_TOKEN_REQUEST_TOKEN` in GitHub Actions. `ARM_OIDC_REQUEST_URL` and
`ARM_OIDC_REQUEST_TOKEN` override them.

A pipeline that fetches its own OIDC token can hand it over in `ARM_OIDC_TOKEN`,
or in a file named by `ARM_OIDC_TOKEN_FILE_PATH`, with `use_oidc` and
`client_id` set. The provider uses either of those before a service connection.
For example, inside `AzureCLI@2` with `addSpnToEnvironment: true`, the script can
`export ARM_OIDC_TOKEN=$idToken`.

## Moving a pipeline off the key

1. Make the pipeline's identity an application user with System Customizer in
   each Dataverse environment.
2. Add the Entra settings above and remove `DATAVERSE_CONTACT_CONNECTION_KEY`
   (or `connection_key`) from that pipeline.
3. Run `terraform plan`. It should report no changes: how the provider signs in
   doesn't affect what it manages.

Pipelines can move one at a time. Once none of them uses the key, remove
`ADMIN_CONNECTION_KEY` from the API deployment and the key stops working
everywhere. The API records an `AdminConnectionKeyValidated` event in its
Application Insights each time the key is used, so you can see when the last
user has moved.

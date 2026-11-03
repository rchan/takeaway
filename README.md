# Takeaway

A small ordering app for a takeaway shop, written in Go with only the standard
library (plus the Google Cloud client for the optional Firestore database).
It is the worked example for the [GoRocket](../gorocket) tutorial.

| Screen | URL | Who |
|---|---|---|
| Order page | `/` | customers |
| Order tracking | `/order/<id>` | customers, updates live |
| Menu admin | `/admin` | owner: add, edit, hide and delete items and prices |
| Order history | `/admin/orders` | owner |
| Kitchen board | `/kitchen` | kitchen: new orders appear automatically |

## Run it

You need [Go](https://go.dev/dl/) 1.26 or newer. Nothing else.

```sh
git clone <this repository>
cd takeaway
ADMIN_PIN=choose-one KITCHEN_PIN=choose-another go run .
```

There are no default PINs: the app refuses to start unless you set both.

Open <http://localhost:8080>. The first run fills the menu with a few demo
dishes and saves everything to `data/takeaway.json`.

Staff sign-in, using the PINs you set:

* Admin: <http://localhost:8080/admin>
* Kitchen: <http://localhost:8080/kitchen>

Open the order page in one window and the kitchen board in another, place an
order, and watch it arrive.

## Settings

All settings are environment variables.

| Variable | Default | Meaning |
|---|---|---|
| `PORT` | `8080` | HTTP port |
| `SHOP_NAME` | `Gopher Takeaway` | name shown in the header |
| `ADMIN_PIN` | required | admin sign-in |
| `KITCHEN_PIN` | required | kitchen sign-in |
| `SESSION_SECRET` | random per start (warns) | signs login cookies; set it in production |
| `STORE` | `file` | `file` or `firestore` |
| `DATA_FILE` | `data/takeaway.json` | file store location |
| `SEED_DEMO` | on | set to `0` to start with an empty menu |
| `GOOGLE_CLOUD_PROJECT` | | required when `STORE=firestore` |

## Tests

```sh
go vet ./... && go test -race -count=1 ./...
```

## Deploy to Google Cloud (App Engine)

The file store cannot be used on App Engine (its disk is read-only and each
instance has its own), so deploy with Firestore.

```sh
gcloud projects create my-takeaway            # or use an existing project
gcloud config set project my-takeaway
gcloud app create --region=europe-west2
gcloud firestore databases create --location=europe-west2

cp env.example.yaml env.yaml                  # then edit the PINs and secret
gcloud app deploy app.yaml
gcloud app browse
```

`env.yaml` holds your PINs, so it is gitignored. For a stricter setup, keep the
PINs in Secret Manager and load them at startup.

## How it is built

* `main.go`: server struct, routes, templates, configuration
* `store.go`: models and the `Store` interface
* `store_file.go`, `store_firestore.go`: the two databases
* `auth.go`: PIN login, signed cookies, CSRF tokens, brute-force lockout
* `handlers_customer.go`, `handlers_staff.go`: the pages
* `templates/`, `static/`: HTML, CSS and JavaScript, embedded in the binary

Prices are whole pence, never floats. The server always looks prices up itself,
so a customer cannot change what an item costs.

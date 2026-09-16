🇬🇧 English · [🇪🇸 Español](INTERFAZ.md) · [← Back](README.md)

# The interface

The five modules of the panel. **The interface is in Spanish, and so are these
images: that is what the product actually shows.** They are mockups with
invented data, not screenshots of any node — each one shows a different case,
which a screenshot cannot do.

## Resumen — overview

What happened today, with each event and the network it came from.

![Overview: daily counters and recent activity](imagenes/ui-resumen.svg)

## Archivos — files

The store, here with an upload in progress: the state a screenshot almost never
catches.

![Files: a listing with an upload at 60 %](imagenes/ui-archivos.svg)

## Estado — health

Node health. Two indicators in amber: the disk at 71 %, and a backup older than
the threshold the client itself publishes.

![Health: seven indicators, two of them amber](imagenes/ui-estado.svg)

## Seguridad — security

Who called and from where, over the same map the node serves — 174 countries,
generated offline and embedded in the binary. On top, the live ribbon fed by
the stream that already exists. Below, the breakdown by network and the
quarantined addresses, which expire on their own.

![Security: origin map, live ribbon, per-network breakdown and two quarantined addresses](imagenes/ui-seguridad.svg)

## Cuentas — accounts

Who may sign in, and how much they hold. Creating and removing accounts works
only from the local network or the tunnel: that rule lives in the route table,
not in this page.

![Accounts: three accounts with last sign-in and disk usage](imagenes/ui-cuentas.svg)

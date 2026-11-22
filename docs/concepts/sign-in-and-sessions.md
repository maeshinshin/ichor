---
title: "Sign-in and sessions"
description: "What a sign-in gives you in Ichor (a 5-minute ID token and a single-use refresh chain), how long a session lasts, and what ends one early."
sidebar_group: "Trust and access"
sidebar_order: 60
---

# Sign-in and sessions

With Ichor's own users, ichor-ui issues an ID token that every request carries
and a refresh token that trades itself for a new ID token. Setup:
[Users and login](../guides/users-and-login.md).

![A sign-in over time, and what ends it early. You type your password and ichor-ui issues an ID token that lasts 5 minutes and refresh token 1. About every 4 minutes the page trades the refresh token for a new pair, and each refresh retires the last token, until the session lifetime of 12 hours by default ends it. Revoking sessions, changing the password or disabling the user makes ichor-ui refuse earlier ID tokens within 10 seconds and refuse the next refresh; kubectl keeps its ID token for up to 5 minutes. Signing out revokes the refresh token at the issuer. Presenting a retired refresh token ends the whole chain, and so does an etcd restore, which rewinds the Secret that holds it.](../images/sign-in-timeline.light.drawio.svg)

*The life of one session, and the four things that end it before its lifetime.*

| | Value |
|---|---|
| ID token | 5 minutes by default (1 minute to 1 hour). ichor-ui checks it against the user's `notBefore` on every request, so a revoke or password change ends earlier tokens. The kube-apiserver cannot check that, so a token already given to `kubectl` lives out its 5 minutes |
| Session lifetime | `ichorUI.users.refreshLifetime`: 12 hours by default, 1 hour to 30 days, counted from the password. A refresh never extends it |
| Refresh token | single-use; its state is in the user's entry of the `ichor-user-credentials` Secret, so all replicas agree |
| Sign-out | best effort: if the issuer cannot be reached it still completes, and the refresh token lasts to the end of its lifetime unless you revoke the user's sessions |

An etcd restore ends sessions as expected, not a fault: sign in again with
`ichor auth login` or the browser. Users, passwords and roles go back to the
snapshot too, so a password changed after it is the old one again. Log and
download streams end with "your sign-in expired" when their token does.

## In the browser

The page trades its refresh token a minute before the ID token ends, when you
return to a tab and when a request is refused; a refused refresh returns the tab
to the sign-in card. The refresh token lives in the tab's `sessionStorage`, so
closing the tab ends the session; each tab, including a duplicated one, signs in
on its own.

There is no idle timeout: an unattended open tab stays signed in until the
session lifetime. Lock the screen, or shorten `refreshLifetime`.

## Wrong passwords slow down

They never lock a user out. The counter is per (client address, user name), so a
stranger cannot lock `admin` out for you.

| Trigger | Effect |
|---|---|
| 5 failures by one address and user | waits 1 s, 2 s, 4 s ... up to 15 minutes |
| 30 failures by one address across names | that address waits too, up to 1 minute |
| 30 failures of one user within an hour, any addresses | every sign-in of that user takes 2 s longer, never refused |

Counters are per replica, so N replicas give N times the free guesses. Behind a
proxy or load balancer the client address is the proxy's unless you list it in
`ichorUI.users.trustedProxyCIDRs` ([Client addresses behind a load
balancer](../guides/users-and-login.md#client-addresses-behind-a-load-balancer)).
Password hashing has its own queue, so a flood of sign-ins gets
`503 Retry-After` and cannot block a password change.

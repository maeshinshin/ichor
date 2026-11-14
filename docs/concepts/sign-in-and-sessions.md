---
title: "Sign-in and sessions"
description: "What a sign-in gives you in Ichor (a 5-minute ID token and a single-use refresh chain), how long a session lasts, and what ends one early."
sidebar_group: "Trust and access"
sidebar_order: 60
---

# Sign-in and sessions

When people sign in with Ichor's own users, ichor-ui issues two tokens: a short
ID token, which every request carries, and a refresh token, which trades itself
for a new ID token. The steps to turn this on are in
[Users and login](../guides/users-and-login.md).

## One session, and what ends it

![A sign-in over time, and what ends it early. You type your password and ichor-ui issues an ID token that lasts 5 minutes and refresh token 1. About every 4 minutes the page trades the refresh token for a new pair, and each refresh retires the last token, until the session lifetime of 12 hours by default ends it. Revoking sessions, changing the password or disabling the user makes ichor-ui refuse earlier ID tokens within 10 seconds and refuse the next refresh; kubectl keeps its ID token for up to 5 minutes. Signing out revokes the refresh token at the issuer. Presenting a retired refresh token ends the whole chain, and so does an etcd restore, which rewinds the Secret that holds it.](../images/sign-in-timeline.light.drawio.svg)

*The life of one session, and the four things that end it before its lifetime.*

An ID token lasts 5 minutes by default (1 minute to 1 hour). ichor-ui checks it
against the user's `notBefore` on every request, which is how a revoke or a
password change ends tokens issued before it. The kube-apiserver cannot do that
check, so a token already given to `kubectl` lives out its 5 minutes there.

The session lifetime is `ichorUI.users.refreshLifetime`: 12 hours by default,
1 hour to 30 days, counted from the password. A refresh never extends it.

A refresh token is single-use. Its state is kept in the user's entry of the
`ichor-user-credentials` Secret, so all replicas agree. Tokens issued before
this existed are accepted once and then rotated.

An etcd restore is expected to end sessions, not a fault: sign in again with
`ichor auth login` or the browser. Users, passwords and roles go back to the
snapshot too, so a password changed after it is the old one again.

Signing out is best effort. If the issuer cannot be reached the sign-out still
completes, and the refresh token then lasts to the end of its lifetime unless
you revoke the user's sessions.

Log and download streams end with "your sign-in expired" when their token does.

## In the browser

The browser stays signed in as long as the CLI would. The page trades its
refresh token a minute before the ID token ends, when you come back to a tab and
when a request is refused. When a refresh is refused, the tab returns to the
sign-in card.

The refresh token lives in the tab's `sessionStorage` next to the ID token, so
closing the tab ends the session. Each tab has its own sign-in, and a tab the
browser duplicates signs in again.

There is no idle timeout: an open tab on an unattended screen stays signed in
until the session lifetime. Lock the screen, or shorten `refreshLifetime`.
Signing in with an external identity provider works as before.

## Wrong passwords slow down

They do not lock a user out. The counter is per (client address, user name), so a
stranger cannot lock `admin` out for you.

- After 5 failures that pair waits 1 s, 2 s, 4 s and so on up to 15 minutes.
- One address waits too after 30 failures across names, up to 1 minute.
- Once a user has 30 failures within an hour from any addresses, every sign-in
  of that user takes 2 s longer, and is never refused.

Counters are kept per replica, so with N replicas the free guesses are N times
as many. Behind a proxy or load balancer the client address is the proxy's
unless you list it in `ichorUI.users.trustedProxyCIDRs`; see
[Client addresses behind a load balancer](../guides/users-and-login.md#client-addresses-behind-a-load-balancer).
Password hashing for sign-in has its own queue, so a flood of sign-ins gets
`503 Retry-After` and cannot block a password change.

# Legal launch checklist

**Status:** blocking checklist for paid launch. Public documents reduce risk;
they do not replace these operational steps.

## P0 — complete before accepting new registrations or payments

- [x] **Confirm the production authentication and database location.** Since
  2026-08-14, production authentication uses the operator-controlled
  self-hosted Supabase endpoint `https://auth.snaphost.ru` on a dedicated
  Russian VDS. The browser bundle and API Gateway both use this endpoint; the
  former managed project is retained only as a time-bounded rollback source
  and no longer receives normal registration traffic.
- [ ] **Comply with Russian-site authorization rules.** Obtain a written legal
  conclusion for the selected method under part 10 of article 8 of 149-FZ.
  Since 2026-07-07, relying on a foreign-owned authorization system carries a
  direct administrative-fine risk.
- [ ] **Submit or update the Roskomnadzor personal-data notification** before
  processing. The notification must match the data map, purposes, database
  locations, processors, security measures and any cross-border transfer.
- [ ] **Obtain a hosting-provider classification opinion.** SnapHost creates
  Internet-connected runtimes for customer applications. If this is activity
  of a hosting provider under article 10.2-1 of 149-FZ, register before
  providing it, implement the prescribed customer-identification method and
  establish the required regulator interaction. Calling the service “PaaS” in
  the offer does not decide the factual classification.
- [ ] **Select the payment/acquiring and fiscalization providers.** Add their
  exact legal names, addresses and processing roles to the privacy documents.
- [ ] **Connect KKT/OFD and validate receipt scenarios.** Test payment,
  renewal, partial refund, full refund and—if coins are treated as an
  advance—the subsequent set-off of the advance. A provider payment receipt is
  not automatically a fiscal receipt.
- [ ] **Publish a tariff allocation.** Every tariff must show the final price
  in roubles, period, base access component, number of paid coins, rouble value
  used to calculate consumed coins on cancellation, limits, expiry and
  auto-renewal status. Without this split the refund formula is not auditable.
- [ ] **Provide self-service cancellation and payment-method removal.** Once a
  consumer refuses use of stored payment details, no recurring charge may use
  them. Store the refusal time and provider acknowledgement.
- [ ] **Create `privacy@snaphost.ru` or formally designate
  `support@snaphost.ru`** as the permanent personal-data contact. The drafts
  currently use the supplied support address.
- [ ] **Add bank details and tax/VAT wording** after the settlement account and
  tax regime are confirmed. The price shown to consumers must be final.

## P0 — product and consent evidence

- [ ] Apply
  [`0003_legal_acceptances.sql`](https://github.com/justaba/snaphost-supabase/blob/main/supabase/migrations/0003_legal_acceptances.sql)
  from the standalone identity repository and verify that a
  signup creates separate acceptance rows with a server timestamp and document
  version.
- [ ] Move acceptance enforcement to the server-side Russian authentication
  boundary. Client-side checkboxes and user metadata alone are evidence, but
  they are not a robust access-control boundary.
- [ ] Prevent first-time OAuth login from bypassing legal acceptance. The
  current GitHub button can create a Supabase user without either signup
  checkbox and is also a foreign authorization method. Disable it for Russian
  users or replace the whole flow with a compliant Russian-owned system before
  launch.
- [ ] Retain the exact text/hash of every accepted document revision.
- [ ] Record renewal consent separately from offer and personal-data consent:
  user ID, tariff, price, billing period, payment-token ID, document version,
  UTC time and request/audit identifier.
- [ ] Do not preselect offer, personal-data, marketing, cookie or recurring-
  payment checkboxes.
- [ ] Add a clear 18+ confirmation and select an identity procedure compatible
  with the hosting-provider conclusion.

## P1 — personal-data governance

- [ ] Approve an internal personal-data processing regulation and appoint the
  person responsible for processing personal data.
- [ ] Approve data-access roles, employee confidentiality obligations,
  deletion/destruction procedure, request-handling procedure and audit plan.
- [ ] Complete the harm assessment, threat model and ISPDn security-level
  determination; implement measures required by 152-FZ, Government Decree
  No. 1119 and applicable FSTEC rules.
- [ ] Sign data-processing clauses with the VDS provider, Yandex Cloud,
  payment provider, OFD/KKT operator and email provider.
- [ ] Document incident response: initial Roskomnadzor notification within the
  statutory window, investigation update, evidence preservation and customer
  communications.
- [ ] Define and technically enforce retention for application logs, proxy
  logs, build logs, deployment metadata, support tickets, deleted accounts and
  backups. Replace the generic periods in the public policy if the real values
  differ.
- [ ] Review GitHub/GitLab/Bitbucket private-repository flows for cross-border
  transfer. If personal data is transferred abroad, complete the separate
  Roskomnadzor procedure before enabling that path.
- [ ] Verify account deletion end to end: Supabase/auth, profiles, wallet,
  projects, domains, API keys, Redis material, Yandex containers, logs and
  backups.

## P1 — hosting and abuse operations

- [ ] Monitor `report@snaphost.ru`; assign an on-call owner and keep an abuse
  decision log.
- [ ] Implement an emergency disable path for phishing, malware, CSAM,
  credential theft and regulator notices, with an operator audit trail.
- [ ] Keep reliable customer-to-deployment and hostname-to-deployment mapping
  for the legally required period.
- [ ] Publish the abuse URL in generated-site error pages and in the footer.
- [ ] Do not promise a response time that cannot be staffed.

## P1 — consumer and payment operations

- [ ] Provide the purchase screen with seller name, INN/OGRN, final price,
  exact service description, billing period, coin allowance, auto-renewal
  checkbox, cancellation method, refund link and receipt email.
- [ ] Send advance notice of renewal as a customer-friendly control even when
  not expressly required for a particular payment method.
- [ ] Provide payment history and fiscal-receipt links in the account.
- [ ] Implement refund calculation from immutable tariff snapshots, not the
  current tariff page.
- [ ] Return money to the original payment method unless law or the provider
  requires another verified procedure.
- [ ] Preserve payment, receipt, acceptance, cancellation and refund records
  for the statutory accounting/tax period.

## P2 — release verification

- [ ] Test every `/legal/*` route without authentication and on mobile.
- [ ] Confirm footer links also appear on login, signup, purchase and dashboard
  screens.
- [ ] Confirm no analytics or marketing requests fire before consent.
- [ ] Have a Russian IT/privacy lawyer review the factual service description,
  hosting-provider status, subscription allocation and refund formula.
- [ ] Have the accountant and KKT provider approve receipt item names, VAT code
  and advance/set-off logic.

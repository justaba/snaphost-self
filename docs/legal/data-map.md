# Personal-data map

This is the working inventory used to keep the public policy, Roskomnadzor
notification and technical implementation consistent.

## Account holder

- Data: UUID, email, password verifier held by the auth provider, display name,
  GitHub username, avatar URL if later enabled, role, signup/confirmation/login
  timestamps, IP address, user agent, session and audit identifiers.
- Purposes: registration, authentication, account administration, security,
  support, contract performance and legal compliance.
- Systems: authentication service, Supabase `profiles`, user-billing `users`,
  gateway/security logs and backups.
- Legal bases: contract steps/performance, operator legal obligations,
  legitimate security purposes within Russian-law limits, and separate consent
  where used.

## Customer projects and operations

- Data: repository URL and branch, project/deployment IDs and status, source
  archive, Git provider username/token, custom domain, build/runtime logs,
  image reference, API-key metadata and hash, support diagnostics.
- Purposes: build, security scan, deployment, routing, deletion, troubleshooting
  and abuse prevention.
- Short-lived material: uploaded archives default to a 15-minute Redis TTL and
  are deleted after unpack; private Git credentials default to a 30-minute TTL
  and are deleted after clone. Production values must be verified before the
  public policy is finalized.
- Recipients/processors: Russian VDS provider, Yandex Cloud, and the source-code
  host expressly selected by the customer.

## Subscription and payment

- Data: user ID, tariff snapshot, rouble amount, coin allowance, payment and
  provider IDs, status, timestamps, receipt email, payment-token reference,
  renewal consent, cancellation/refund records. SnapHost must not receive or
  store full card number or CVC.
- Purposes: payment, fiscal receipts, recurring billing, refunds,
  reconciliation, fraud prevention, accounting and disputes.
- Systems/recipients: user-billing ledger, selected acquiring provider,
  KKT/OFD provider, accounting records and backups.

## Visitors to customer applications

- Data potentially processed in transit: IP address, host, URL, headers,
  request/response timestamps and content, cookies sent to the customer
  application, error/security logs.
- Roles: the customer normally determines the purposes of its application and
  acts as personal-data operator; ООО «ГОУДОНАТ» processes data on the
  customer's documented instructions under the customer-data processing
  terms. SnapHost remains an independent operator for platform security and
  statutory abuse handling.

## Support and abuse reporters

- Data: name or company name if supplied, email, URLs, complaint text,
  attachments/evidence, correspondence, decision and timestamps.
- Purposes: answer requests, protect rights, investigate abuse, preserve
  evidence and comply with regulator/court notices.

## Deliberately excluded

- special-category and biometric personal data;
- payment-card CVC and full card number;
- advertising profiles and behavioural analytics;
- customer source code or secrets in ordinary application logs.

If these appear because a customer embeds them in an archive, repository,
environment value or log, they remain customer content and must be handled
under the data-processing terms and incident procedure.

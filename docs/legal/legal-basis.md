# Legal basis and review notes

**Checked:** 2026-08-10. This is an internal implementation note, not a legal
opinion and not a public contract.

## Personal data

- [Federal Law No. 152-FZ](https://www.consultant.ru/document/cons_doc_LAW_61801/)
  governs purposes, legal bases, security, data-subject rights and operator
  duties.
- [Article 18(5)](https://www.consultant.ru/document/cons_doc_LAW_61801/cbf4e15b7c330f9372e876cdf2bc928bad7950ef/)
  requires the specified primary database operations involving personal data
  of Russian citizens to use databases in Russia.
- [Article 22](https://www.consultant.ru/document/cons_doc_LAW_61801/d996966e22e1320c9de1ab82d9f6be12c3d9d765/)
  generally requires notification of Roskomnadzor before processing.
- Since 2025-09-01, article 9 requires personal-data consent to be оформлено
  separately from other accepted or signed information and documents. This is
  why signup has a dedicated unchecked control and a standalone consent text.

## Authorization and hosting

- Part 10 of article 8 of Federal Law No. 149-FZ restricts the authorization
  methods used by Russian resource owners. Federal Law No. 199-FZ of
  2026-06-26 introduced administrative fines effective 2026-07-07. Production
  Supabase Auth and GitHub OAuth require replacement or an individualized legal
  conclusion before Russian registrations continue.
- [Article 10.2-1 of Federal Law No. 149-FZ](https://www.consultant.ru/document/cons_doc_LAW_61798/b99fcec60c7460838ae71cdd49a1bac4332fcb12/)
  regulates providers that supply computing capacity for placing information
  in an Internet-connected information system. SnapHost's actual deployment
  model should be classified by Russian counsel; a product label cannot change
  the substance of the service.

## Consumers, subscriptions and fiscalization

- [Article 32 of the Consumer Protection Law](https://www.consultant.ru/document/cons_doc_LAW_305/758e2cfdf136a621c8f66dcb3372b772c7b5e6e8/)
  lets a consumer cancel a service contract at any time subject to payment of
  the contractor's actually incurred expenses connected with that contract.
- Federal Law No. 376-FZ of 2025-10-15 prohibits recurring charges using
  payment details after the consumer has refused their further use. The product
  therefore needs an electronic cancellation/removal path and provider-side
  confirmation.
- Federal Law No. 54-FZ and current FNS guidance govern online KKT receipts.
  Acquiring confirmation is not necessarily a fiscal receipt. The accountant
  and fiscalization provider must validate the advance and set-off scenarios
  for the final coin model.

## Drafting references

The public-document structure was compared with the legal sections of Amvera,
RelaxDev and ONREZA. Their wording was not copied: SnapHost has a different
consumer-only audience, subscription/coin model, deployment flow and data map.

# SnapHost legal package

**Status:** draft prepared on 2026-08-10. Legal review and the launch gates in
[launch-checklist.md](launch-checklist.md) are required before paid launch.

This directory describes the legal surface for the Russian version of
SnapHost. The public documents are versioned with the frontend because the
acceptance UI records their exact revision:

- [Public offer](https://github.com/justaba/snaphost-ui/blob/main/src/content/legal/offer.md)
- [Personal-data policy](https://github.com/justaba/snaphost-ui/blob/main/src/content/legal/privacy.md)
- [Personal-data consent](https://github.com/justaba/snaphost-ui/blob/main/src/content/legal/consent.md)
- [Cookie and local-storage policy](https://github.com/justaba/snaphost-ui/blob/main/src/content/legal/cookies.md)
- [Acceptable use policy](https://github.com/justaba/snaphost-ui/blob/main/src/content/legal/acceptable-use.md)
- [Abuse-report procedure](https://github.com/justaba/snaphost-ui/blob/main/src/content/legal/abuse.md)
- [Payment, cancellation and refund rules](https://github.com/justaba/snaphost-ui/blob/main/src/content/legal/refunds.md)
- [Customer-data processing terms](https://github.com/justaba/snaphost-ui/blob/main/src/content/legal/data-processing.md)
- [Company details and contacts](https://github.com/justaba/snaphost-ui/blob/main/src/content/legal/details.md)
- [Legal basis and review notes](legal-basis.md)

## Operator

- Full name: Общество с ограниченной ответственностью «ГОУДОНАТ»
- Short name: ООО «ГОУДОНАТ»
- INN: 0261067909
- KPP: 026101001
- OGRN: 1240200033260
- Registered address: 453266, Республика Башкортостан, г. Салават,
  ул. Калинина, д. 100, кв. 38
- Support and personal-data requests: support@snaphost.ru
- Abuse reports: report@snaphost.ru

The KPP and normalized address were cross-checked against public corporate
registry data. Bank details are intentionally absent: they must be added after
the acquiring bank and settlement account are selected.

## Product assumptions used by the drafts

- customers are natural persons aged 18 or older and located in Russia;
- a paid plan is an automatically renewable subscription combining access to
  the platform with a disclosed allowance of internal usage units (“coins”);
- coins are not transferable and are not money, electronic money,
  cryptocurrency or a cash substitute;
- non-spent paid allowance is accounted for when a consumer cancels during a
  billing period; bonus units have no cash value;
- control-plane and primary databases must be in Russia; user applications run
  in Yandex Cloud in Russia;
- there is no advertising analytics, Sentry, CAPTCHA, marketing mailing or
  live-chat integration in the audited frontend;
- non-essential tracking must remain disabled until a consent manager and an
  updated cookie policy are deployed.

## Versioning rule

The current public-document version is `2026-08-10`. A substantive change to
the offer, AUP or personal-data consent requires all of the following:

1. update the relevant Markdown document and its displayed revision;
2. update `LEGAL_VERSION` in
   `justaba/snaphost-ui:src/content/legal/version.ts`;
3. update the signup metadata version and the Supabase acceptance migration;
4. preserve the old document text and existing acceptance records;
5. require a new affirmative acceptance when the legal basis or scope of
   processing materially changes.

Do not overwrite an old revision in place after customers have accepted it.

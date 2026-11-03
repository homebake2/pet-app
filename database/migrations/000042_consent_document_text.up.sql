-- Актуальные тексты документов (отдаются GET /consent-documents). Текст и версия
-- документа в consent_document обновляются вместе, в одной транзакции.
-- doc_type: terms — условия использования, personal_data — политика
-- конфиденциальности, на условиях которой дано согласие на обработку.
CREATE TABLE consent_document_text (
    doc_type text NOT NULL REFERENCES consent_document(doc_type),
    language text NOT NULL CHECK (language IN ('ru', 'en')),
    text     text NOT NULL,
    PRIMARY KEY (doc_type, language)
);

INSERT INTO consent_document_text (doc_type, language, text) VALUES ('terms', 'ru', $doc$Последнее обновление: 2 ноября 2026 г.

Эти условия регулируют использование мобильного приложения PetHealth («Приложение») и сайта («Сайт»). Начиная пользоваться Приложением или Сайтом, вы принимаете эти условия. Если вы с ними не согласны, не пользуйтесь Приложением и Сайтом. Обработку персональных данных описывает отдельная Политика конфиденциальности.

## 1. Оператор

Приложение и Сайт предоставляет Иванов Иван Иванович, самозанятый (плательщик налога на профессиональный доход), ИНН 000000000000 («Оператор»).

Связаться с Оператором можно по адресу test@test.ru.

## 2. Что делает Приложение

Приложение помогает вести учёт данных о питомцах: карточки, события, напоминания, записи ветеринарного паспорта, графики динамики.

Это справочный инструмент, а не ветеринарная услуга. Приложение не ставит диагнозы, не назначает лечение и не заменяет ветеринара. Решения о здоровье и лечении питомца вы принимаете сами и под свою ответственность.

Напоминания формирует ваше устройство. Оператор не гарантирует, что они будут показаны вовремя.

## 3. Аккаунт и режимы работы

- Пользоваться Приложением могут лица не младше 16 лет.
- Сетевой аккаунт создаётся по логину и паролю. Вы отвечаете за сохранность пароля и за действия под своим аккаунтом. Если вы подозреваете несанкционированный доступ, сообщите об этом Оператору.
- В локальном режиме данные остаются только на вашем устройстве. Их сохранность, в том числе при удалении Приложения или потере устройства, зависит от вас.
- Удалить аккаунт можно в настройках Приложения в любой момент.

## 4. Ваши данные

Данные, которые вы вносите в Приложение (клички, заметки, фотографии, файлы, записи), остаются вашими. Вы даёте Оператору право хранить и обрабатывать их только в объёме, нужном для работы Приложения: хранить, показывать вам и тем, кому вы открыли доступ, и технически обрабатывать.

Вы подтверждаете, что вправе вносить эти данные. Не вносите данные других лиц без их согласия, незаконные материалы и материалы, нарушающие чужие права.

## 5. Совместный доступ и передача питомца

Приглашая другого человека или передавая ему питомца, вы сами решаете, кому показать данные. Человек с доступом видит ваше имя, данные питомца и список участников доступа.

Роль «только просмотр» не позволяет изменять записи, роль «полный доступ» позволяет. Владелец питомца может завершить доступ в любой момент. При передаче питомец и его записи переходят к другому пользователю.

Оператор не проверяет личность участников и не отвечает за их действия.

## 6. Реклама и поддержка проекта

В Приложении есть необязательный раздел с рекламой третьей стороны. Оператор не отвечает за содержание рекламы и за товары и услуги рекламодателей.

Поддержка проекта на Сайте добровольна и безвозвратна. Она не даёт права на дополнительные функции или услуги.

## 7. Что делать нельзя

- нарушать закон и права других лиц;
- пытаться получить доступ к чужим данным, подбирать пароли, коды передачи и приглашений;
- нарушать работу Приложения, Сайта и сервера, обходить ограничения, автоматически собирать данные;
- выдавать себя за другое лицо.

## 8. Доступность и изменения

Приложение и Сайт предоставляются «как есть». Возможны перерывы в работе и сбои.

Оператор может менять и прекращать отдельные функции. О существенных изменениях он сообщит в Приложении или на Сайте. Не храните единственную копию важных документов только в Приложении.

## 9. Ответственность

В пределах, допускаемых законом, Оператор не отвечает за последствия решений о здоровье и лечении питомца, принятых на основе данных Приложения, за недоступность Приложения по независящим от него причинам и за действия других пользователей.

Эти условия не ограничивают права потребителя, которые закон закрепляет и которые нельзя исключить договором.

## 10. Ограничение доступа и прекращение использования

Оператор вправе ограничить доступ к аккаунту при нарушении этих условий или закона и по возможности сообщит причину.

Вы можете прекратить пользоваться Приложением в любой момент, удалив аккаунт. Данные удаляются в сроки, указанные в Политике конфиденциальности.

## 11. Применимое право и споры

К этим условиям применяется право Российской Федерации. Если вы потребитель и живёте в другой стране, в том числе в государстве — члене Европейского союза, вам по-прежнему гарантирована защита обязательных норм этой страны.

Претензии направляйте на адрес Оператора, мы ответим в течение 30 дней. Если спор решить не удалось, он рассматривается в суде в порядке, предусмотренном законом.

## 12. Изменения условий

Оператор может обновлять эти условия. Актуальная версия публикуется в Приложении и на Сайте, дата последнего обновления указана вверху. Продолжая пользоваться Приложением или Сайтом после изменений, вы принимаете новую редакцию.
$doc$);

INSERT INTO consent_document_text (doc_type, language, text) VALUES ('terms', 'en', $doc$Last updated: November 2, 2026

These terms govern the use of the PetHealth mobile app (the “App”) and the website (the “Site”). By starting to use the App or the Site, you accept these terms. If you do not agree, do not use the App or the Site. The processing of personal data is described in the separate Privacy Policy.

## 1. Operator

The App and the Site are provided by Ivan Ivanovich Ivanov, self-employed individual (professional income tax payer), tax ID 000000000000 (the “Operator”).

You can contact the Operator at test@test.ru.

## 2. What the App does

The App helps you keep records about your pets: profiles, events, reminders, veterinary passport entries and trend charts.

It is a reference tool, not a veterinary service. The App does not diagnose, prescribe treatment or replace a veterinarian. You make decisions about your pet's health and treatment yourself and at your own risk.

Reminders are generated by your device. The Operator does not guarantee that they will be shown on time.

## 3. Account and modes

- You must be at least 16 years old to use the App.
- A network account is created with a login and a password. You are responsible for keeping your password safe and for actions under your account. If you suspect unauthorized access, tell the Operator.
- In local mode, data stays only on your device. Keeping it safe, including when the App is deleted or the device is lost, is up to you.
- You can delete your account in the App settings at any time.

## 4. Your data

The data you enter into the App (names, notes, photos, files, records) remains yours. You grant the Operator the right to store and process it only as needed to run the App: to store it, to show it to you and to people you give access to, and to process it technically.

You confirm that you have the right to enter this data. Do not enter other people's data without their consent, unlawful material or material that infringes the rights of others.

## 5. Shared access and pet transfer

When you invite another person or transfer a pet to them, you decide who sees the data. A person with access sees your name, the pet's data and the list of people with access.

The “view only” role does not allow changing records; the “full access” role does. A pet's owner can end access at any time. On transfer, the pet and its records pass to the other user.

The Operator does not verify the identity of participants and is not responsible for their actions.

## 6. Ads and supporting the project

The App has an optional section with third-party ads. The Operator is not responsible for the content of ads or for advertisers' goods and services.

Supporting the project on the Site is voluntary and non-refundable. It does not entitle you to extra features or services.

## 7. What you must not do

- break the law or infringe the rights of others;
- try to access other people's data or guess passwords, transfer codes or invitation codes;
- disrupt the App, the Site or the server, bypass restrictions or collect data automatically;
- impersonate another person.

## 8. Availability and changes

The App and the Site are provided “as is”. Interruptions and failures are possible.

The Operator may change or discontinue individual features and will announce significant changes in the App or on the Site. Do not keep the only copy of important documents solely in the App.

## 9. Liability

To the extent permitted by law, the Operator is not liable for the consequences of decisions about a pet's health and treatment made on the basis of the App's data, for the App being unavailable for reasons beyond its control, or for the actions of other users.

These terms do not limit consumer rights that the law guarantees and that cannot be excluded by contract.

## 10. Restriction of access and termination

The Operator may restrict access to an account if these terms or the law are violated and will state the reason where possible.

You can stop using the App at any time by deleting your account. Data is deleted within the periods stated in the Privacy Policy.

## 11. Governing law and disputes

These terms are governed by the law of the Russian Federation. If you are a consumer living in another country, including a European Union member state, you still have the protection of the mandatory rules of that country.

Send claims to the Operator's address; we will reply within 30 days. If a dispute cannot be resolved, it is heard in court as provided by law.

## 12. Changes to the terms

The Operator may update these terms. The current version is published in the App and on the Site, with the date of the last update at the top. By continuing to use the App or the Site after a change, you accept the new version.
$doc$);

INSERT INTO consent_document_text (doc_type, language, text) VALUES ('personal_data', 'ru', $doc$Последнее обновление: 2 ноября 2026 г.

Эта политика описывает, какие данные обрабатывают мобильное приложение PetHealth («Приложение») и сайт («Сайт»), для чего, кому они передаются и какие права есть у вас. Используя Приложение или Сайт, вы соглашаетесь с этой политикой.

## 1. Оператор данных

Оператор персональных данных — Иванов Иван Иванович, самозанятый (плательщик налога на профессиональный доход), ИНН 000000000000.

Связаться с оператором можно по адресу test@test.ru.

## 2. Где хранятся данные в Приложении

Приложение работает в одном из двух режимов, который вы выбираете на экране входа.

- Локальный режим: данные хранятся только на вашем устройстве и на сервер не передаются. Они удаляются вместе с приложением или при удалении локального аккаунта.
- Сетевой режим: данные хранятся на сервере, привязаны к вашему аккаунту и доступны на других ваших устройствах.

Данные локального аккаунта попадают на сервер только если вы сами переносите их в сетевой аккаунт.

## 3. Какие данные мы обрабатываем

Аккаунт и профиль (сетевой режим):

- логин и пароль (пароль хранится в виде хеша, а не открытым текстом);
- имя, фамилия, отчество, адрес электронной почты и телефон, если вы их указали.

Данные о питомцах:

- кличка, вид, порода, пол, дата рождения, окрас, вес, номера чипа и кольца, заметки и фотография;
- события, напоминания и записи ветеринарного паспорта: вакцинации, заболевания, визиты к ветеринару, аллергии, лекарства, а также прикреплённые файлы.

Это сведения о питомцах, но поля вроде заметок могут содержать и ваши данные. Не вносите в них лишнего.

Совместный доступ и передача питомца: когда вы приглашаете другого человека или передаёте ему питомца, он видит ваше имя, данные питомца и список участников доступа.

Фото и камера: Приложение просит доступ к камере и фотографиям только для снимка питомца и сканирования QR-кода. Доступ можно отозвать в настройках устройства.

Технические данные: IP-адрес, модель устройства, версия операционной системы и Приложения, журналы запросов к серверу.

Отчёты об ошибках: при сбоях Приложение отправляет в сервис Sentry технические сведения (тип ошибки, версия, модель устройства). Персональные данные пользователя в эти отчёты по умолчанию не включаются.

Реклама: в Приложении есть необязательный раздел с рекламой. Рекламу показывает сеть Яндекса (Yandex Mobile Ads). Перед первым показом Приложение спрашивает ваше согласие. С согласием рекламная сеть может использовать рекламный идентификатор и технические данные устройства, чтобы подбирать рекламу. Без согласия реклама остаётся неперсонализированной. На iOS дополнительно запрашивается разрешение на отслеживание.

Данные Сайта:

- форма «Новые идеи»: категория, заголовок, текст и контакт для ответа (контакт необязателен). Вместе с предложением сохраняется идентификатор пользователя, если вы открыли Сайт из Приложения (идентификатор аккаунта или «local»), иначе «anonymous»;
- при открытии Сайта из Приложения в браузере сохраняются тема, язык и идентификатор пользователя (cookie на один год).

## 4. Для чего мы используем данные

- чтобы Приложение работало: вход, хранение данных о питомцах, календарь, напоминания, совместный доступ;
- чтобы связываться с вами по вашим обращениям и предложениям;
- чтобы находить и исправлять ошибки, обеспечивать безопасность и улучшать сервис;
- чтобы показывать рекламу в необязательном разделе Приложения;
- чтобы принимать добровольную поддержку проекта.

Мы не продаём ваши данные и не используем их для рекламных рассылок.

## 5. Уведомления

Напоминания о событиях питомцев показываются как уведомления, которые формирует само устройство по расписанию. Для этого Приложение просит разрешение на уведомления, его можно отозвать в настройках устройства.

## 6. Кому мы передаём данные

- другим пользователям — только по вашему приглашению или при передаче питомца (см. раздел 3);
- поставщикам инфраструктуры, которые обеспечивают работу Приложения и Сайта (серверы, хостинг Сайта);
- Sentry — отчёты об ошибках;
- ООО «Яндекс» (Россия) — для показа рекламы, в объёме, описанном в разделе 3;
- платёжному сервису — при добровольной поддержке проекта картой или через СБП. Данные карты вводятся на странице сервиса, мы их не видим и не храним;
- государственным органам — если этого требует закон.

Часть этих сервисов может обрабатывать данные за пределами России. Сайт размещён на платформе Vercel.

## 7. Сколько мы храним данные

- Данные сетевого аккаунта хранятся, пока существует аккаунт.
- Удалить аккаунт можно в настройках Приложения. При этом безвозвратно удаляются профиль, питомцы, события и записи ветеринарного паспорта.
- Питомцы, события и записи ветпаспорта, которые вы удалили по отдельности, скрываются из Приложения и через 90 суток удаляются с сервера безвозвратно вместе с файлами. Напоминания удаляются сразу.
- Загрузки файлов, которые не были завершены, удаляются через 24 часа. Файлы, у которых больше нет владельца, например после удаления аккаунта, удаляются при ближайшей автоматической очистке.
- IP-адреса для ограничения частоты регистрации хранятся не дольше 2 суток, а для ограничения неудачных попыток входа — не дольше суток после окончания окна подсчёта.
- Предложения с Сайта хранятся 24 месяца или до удаления аккаунта, с которого они отправлены, если это произошло раньше. Вы можете попросить удалить своё предложение раньше, написав на адрес оператора.
- Резервные копии базы данных и журналы поставщиков инфраструктуры могут сохраняться у них ограниченное время после удаления данных.
- Cookie Сайта хранятся один год и удаляются в настройках браузера.

## 8. Ваши права

Вы можете запросить доступ к своим данным, их исправление или удаление и отозвать согласие на обработку, написав на адрес оператора. Данные профиля и питомцев можно изменять и удалять прямо в Приложении. Как удалить аккаунт, в том числе по письму, описано на странице «Удаление аккаунта» на Сайте.

Решение о рекламе хранится на вашем устройстве. Изменить его можно в любой момент кнопкой «Согласие» на экране «Реклама»: отзыв действует сразу. На iOS разрешение на отслеживание можно отозвать в настройках устройства.

## 9. Поддержка проекта на Сайте

Поддержка проекта добровольна и безвозвратна. Взамен ничего не предоставляется, на функции Приложения она не влияет. Получатель — оператор, указанный в разделе 1. Адреса криптовалютных кошельков, указанные на Сайте, публичны.

## 10. Безопасность

Мы принимаем разумные меры защиты данных, но ни один способ передачи и хранения информации не бывает абсолютно надёжным.

## 11. Дети

Приложение и Сайт не предназначены для лиц младше 16 лет. Мы сознательно не собираем их данные. Если вы считаете, что ребёнок передал нам данные, напишите оператору, и мы их удалим.

## 12. Ссылки на другие сайты

Приложение и Сайт могут содержать ссылки на сторонние ресурсы. Мы не отвечаем за их содержание и политику конфиденциальности.

## 13. Изменения политики

Мы можем обновлять эту политику. Актуальная версия публикуется на этой странице и в Приложении, дата последнего обновления указана вверху.
$doc$);

INSERT INTO consent_document_text (doc_type, language, text) VALUES ('personal_data', 'en', $doc$Last updated: November 2, 2026

This policy describes what data the PetHealth mobile app ("App") and website ("Site") process, why, who receives it, and what rights you have. By using the App or the Site, you agree to this policy.

## 1. Data operator

The personal data operator is Ivan Ivanovich Ivanov, a self-employed individual (professional income tax payer), taxpayer ID 000000000000.

You can contact the operator at test@test.ru.

## 2. Where App data is stored

The App works in one of two modes, which you choose on the sign-in screen.

- Local mode: data is stored only on your device and is not sent to a server. It is deleted together with the app or when you delete the local account.
- Network mode: data is stored on a server, tied to your account and available on your other devices.

Local account data reaches the server only if you transfer it to a network account yourself.

## 3. What data we process

Account and profile (network mode):

- login and password (the password is stored as a hash, not as plain text);
- first name, last name, middle name, email address and phone number, if you provided them.

Pet data:

- name, species, breed, gender, date of birth, color, weight, microchip and ring numbers, notes and a photo;
- events, reminders and veterinary passport records: vaccinations, diseases, vet visits, allergies, medications, plus attached files.

This is information about pets, but fields such as notes may also contain your own data. Do not put anything unnecessary in them.

Shared access and pet transfer: when you invite another person or transfer a pet to them, they see your name, the pet's data and the list of people with access.

Photos and camera: the App asks for camera and photo access only to take a photo of your pet and to scan a QR code. You can revoke access in your device settings.

Technical data: IP address, device model, operating system and App version, server request logs.

Error reports: when the App crashes, it sends technical details (error type, version, device model) to the Sentry service. User personal data is not included in these reports by default.

Ads: the App has an optional section with ads. Ads are served by the Yandex network (Yandex Mobile Ads). Before the first ad is shown, the App asks for your consent. With consent, the ad network may use the advertising identifier and technical device data to select ads. Without consent, ads stay non-personalized. On iOS, a tracking permission is requested as well.

Site data:

- the "New ideas" form: category, title, text and a contact for a reply (the contact is optional). The suggestion is stored together with a user identifier if you opened the Site from the App (an account ID or "local"), otherwise "anonymous";
- when you open the Site from the App, your browser stores the theme, language and user identifier (a cookie for one year).

## 4. What we use the data for

- to make the App work: sign-in, storing pet data, the calendar, reminders, shared access;
- to get back to you about your requests and suggestions;
- to find and fix bugs, keep the service secure and improve it;
- to show ads in the optional section of the App;
- to accept voluntary support for the project.

We do not sell your data and do not use it for advertising mailings.

## 5. Notifications

Reminders about pet events are shown as notifications that your device schedules and delivers itself. The App asks for notification permission for this; you can revoke it in your device settings.

## 6. Who we share data with

- other users — only at your invitation or when a pet is transferred (see section 3);
- infrastructure providers that run the App and the Site (servers, Site hosting);
- Sentry — error reports;
- Yandex LLC (Russia) — to show ads, to the extent described in section 3;
- a payment service — when you support the project by card or the Faster Payments System. Card details are entered on the service's page; we never see or store them;
- government authorities — when required by law.

Some of these services may process data outside Russia. The Site is hosted on the Vercel platform.

## 7. How long we keep data

- Network account data is kept for as long as the account exists.
- You can delete your account in the App settings. This permanently deletes the profile, pets, events and veterinary passport records.
- Pets, events and veterinary passport records that you deleted individually are hidden from the App and permanently deleted from the server with their files after 90 days. Reminders are deleted immediately.
- Uploads of files that were not completed are deleted after 24 hours. Files that no longer have an owner, for example after an account is deleted, are deleted by the next automatic cleanup.
- IP addresses used to limit registration frequency are kept for no longer than 2 days, and those used to limit failed sign-in attempts for no longer than a day after the counting window ends.
- Suggestions from the Site are kept for 24 months, or until the account they were sent from is deleted, if that happens earlier. You can ask for your suggestion to be deleted sooner by writing to the operator.
- Database backups and the logs of infrastructure providers may be kept by them for a limited time after the data is deleted.
- Site cookies are kept for one year and can be removed in your browser settings.

## 8. Your rights

You can request access to your data, its correction or deletion, and withdraw your consent to processing by writing to the operator. You can edit and delete profile and pet data directly in the App. How to delete your account, including by email, is described on the “Account deletion” page of the Site.

Your ads decision is stored on your device. You can change it at any time with the “Consent” button on the Ads screen: a withdrawal takes effect immediately. On iOS, you can revoke the tracking permission in your device settings.

## 9. Supporting the project on the Site

Supporting the project is voluntary and non-refundable. Nothing is provided in return, and it does not affect App features. The recipient is the operator named in section 1. The cryptocurrency wallet addresses shown on the Site are public.

## 10. Security

We take reasonable measures to protect data, but no method of transmitting or storing information is completely secure.

## 11. Children

The App and the Site are not intended for people under 16. We do not knowingly collect their data. If you believe a child has given us data, write to the operator and we will delete it.

## 12. Links to other sites

The App and the Site may contain links to third-party resources. We are not responsible for their content or privacy policies.

## 13. Changes to this policy

We may update this policy. The current version is published on this page and in the App, and the date of the last update is shown at the top.
$doc$);


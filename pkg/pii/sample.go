package pii

import "strings"

// The sample is a complete reference of what a deployment detects, and it has to
// stay one.
//
// It is what an operator reads to check their own data shape is covered, so a
// gap in it reads as a gap in the engine. Two tests hold it, and they run for
// every locale in the registry: one sweeps the live catalogue, so a new category
// with no line in its sample fails; the other is an explicit table of every
// notation the patterns accept, so widening or narrowing a pattern means adding
// or moving a row.
//
// The tables are deliberately not derived from the detector. A derived
// expectation agrees with whatever the detector does, including a form it
// silently stopped reading.
//
// Every value here is fabricated. The identifiers that carry checksums carry
// real ones — a value that failed its own checksum would be rejected by the very
// validation the sample exists to demonstrate — so they are built to verify
// while belonging to nobody.

const internationalSample = `=== Contact et comptes ===
Email : claire.moreau@example.fr, avec alias claire+juridique@example.fr
Email accentué : andré.muller@example.fr
Carte : 4532015112830366, aussi écrite 4532 0151 1283 0366 ou 4532-0151-1283-0366
Cartes des autres longueurs : 4222222222222 (13), 4532015112830366120 (19), 4532 0151 1283 0366 120 (19 groupée)
Carte Mastercard, les deux séries : 5425233430109903 (51-55), 2221000000000009 (série 2, émise depuis 2017)
IBAN : FR1420041010050500013M02606, groupé FR14 2004 1010 0505 0001 3M02 606
IBAN étrangers : DE89370400440532013000, BE68539007547034, NL91ABNA0417164300
Serveur : 192.168.13.42, et 10.0.0.1 en secours
Serveur IPv6 : fd00:1234:5678:0000:0000:8a2e:0370:7334, forme compacte fd00:1234::1, compactée au milieu fd00:1234:5678::8a2e:370:7334
Identifiant document : 507f1f77bcf86cd799439011

=== Dates au format ISO ===
LA MÊME DATE dans ses deux écritures ISO : 1987-03-14, 1987/03/14
Autres dates ISO : 1998-11-30, 2019/03/01, 1977-04-04, 2001-07-08
`

const franceSample = `=== Identité française ===
NIR : 2 69 05 49 588 157 80, forme compacte 184037511600176
SIREN : 443061841, aussi écrit 732 829 320
SIRET : 552 100 554 00013
Téléphone : 06 12 34 56 78, 01.45.67.89.10, +33 1 42 68 53 00, +33 (0)1 42 68 53 00
Adresse : 12 rue de la Paix, 75002 Paris
Adresse abrégée : 12 r. de la Paix, 75002 Paris
Adresse sans numéro : Route de Lyon, 38000 Grenoble
Numéro complété : 12 bis rue de la Paix, 14 ter avenue de la Paix, 16 quater place de la Paix

=== Types de voie ===
Les vingt-deux orthographes acceptées, en entier : 1 avenue de l'Exemple, 2 allée de l'Exemple, 3 boulevard de l'Exemple, 4 chemin de l'Exemple, 5 cours de l'Exemple, 6 impasse de l'Exemple, 7 place de l'Exemple, 8 quai de l'Exemple, 9 route de l'Exemple, 10 rue de l'Exemple, 11 square de l'Exemple
Les mêmes en abrégé : 12 av. de l'Exemple, 13 all. de l'Exemple, 14 bd de l'Exemple, 15 bd. de l'Exemple, 16 ch. de l'Exemple, 17 crs de l'Exemple, 18 imp. de l'Exemple, 19 pl. de l'Exemple, 20 rte de l'Exemple, 21 r. de l'Exemple, 22 sq. de l'Exemple

=== Codes postaux ===
Commune en un mot : 69001 Lyon
Commune composée : 13100 Aix-en-Provence
Commune en plusieurs mots : 13290 Aix Les Milles
Capitale accentuée : 91150 Étampes
Bornes des départements, 01 et 98 : 01000 Bourg, 98000 Monaco
Plaque : AB-123-CD

=== Dates de naissance (jour d'abord) ===
LA MÊME DATE dans ses onze notations : 23 février 2004, 23 Février 2004, 23 fevrier 2004, 23/02/2004, 23/2/2004, 23-02-2004, 23-2-2004, 23.02.2004, 23.2.2004, 23 02 2004, 23 2 2004
LA MÊME DATE avec et sans zéro initial : 9 mars 2004, 09 mars 2004, 9/3/2004, 09/03/2004, 9/03/2004, 09/3/2004, 9-3-2004, 9.3.2004, 9 3 2004
Les douze noms de mois : 9 janvier 2004, 17 février 1998, 1er mars 2019, 4 avril 1977, 12 mai 1985, 30 juin 1962, 8 juillet 2001, 22 août 1993, 3 septembre 1970, 15 octobre 1988, 26 novembre 1955, 31 décembre 1978
Mois sans accent : 5 aout 1999, 7 decembre 1980, 3 fevrier 1971
Jours 30-31 et mois 10-12 : 30-10-1961, 31.12.1988, 25/11/1999, 31/12/2000
`

const unitedKingdomSample = `=== United Kingdom ===
NHS number: 943 476 5919, also written 9434765919
National Insurance: AB 12 34 56 C, compact AB123456C, without suffix AB123456
Postcode: SW1A 1AA, M1 1AE, EC1A 1BB, B33 8TH, DN55 1PT, CR2 6XH
Telephone: 020 7946 0958, 0161 496 0000, 07700 900123, 02079460958, +44 20 7946 0958
Address with town and postcode: 10 Downing Street, London SW1A 2AA
Address with a letter on the number: 221B Baker Street, London NW1 6XE
Address with no town: 42 Wellington Crescent
Abbreviated street type: 8 High St, Manchester M1 2AB
Street types, written in full: 1 Example Street, 2 Example Road, 3 Example Avenue, 4 Example Lane, 5 Example Close, 6 Example Drive, 7 Example Place, 8 Example Court, 9 Example Crescent, 10 Example Gardens
And the rest: 11 Example Terrace, 12 Example Square, 13 Example Mews, 14 Example Grove, 15 Example Parade, 16 Example Way, 17 Example St, 18 Example Rd, 19 Example Ave

=== Dates of birth (day first) ===
THE SAME DATE in its four notations: 14/03/1987, 14-03-1987, 14.03.1987, 14 March 1987
With and without a leading zero: 4/3/1987, 04/03/1987, 4 March 1987, 04 March 1987
The twelve month names: 9 January 2004, 17 February 1998, 1 March 2019, 4 April 1977, 12 May 1985, 30 June 1962, 8 July 2001, 22 August 1993, 3 September 1970, 15 October 1988, 26 November 1955, 31 December 1978
Month names are read whatever their case: 3 september 1970, 3 SEPTEMBER 1970
`

const unitedStatesSample = `=== United States ===
Social security: 123-45-6789
Employer id: 12-3456789
Routing number: 021000021, also 011000015
Telephone: (555) 234-5678, 555-234-5678, 5552345678, +1 555 234 5678
Address: 123 Main St, Springfield, IL 62704
Address without a city: 456 Oak Avenue
Street types, written in full: 1 Example Street, 2 Example Avenue, 3 Example Boulevard, 4 Example Road, 5 Example Drive, 6 Example Lane, 7 Example Court, 8 Example Place, 9 Example Terrace, 10 Example Parkway, 11 Example Circle, 12 Example Highway, 13 Example Way
The same, abbreviated: 14 Example St, 15 Example Ave, 16 Example Blvd, 17 Example Rd, 18 Example Dr, 19 Example Ln, 20 Example Ct, 21 Example Pl, 22 Example Ter, 23 Example Pkwy, 24 Example Cir, 25 Example Hwy
ZIP with a state: IL 62704, CA 90210, NY 10001, TX 75001, DC 20500
ZIP plus four: 62704-1234

=== Dates (month first) ===
THE SAME DATE in its three notations: 03/14/1987, 03-14-1987, 03.14.1987
With and without a leading zero: 3/14/1987, 03/14/1987
Other dates: 12/25/2024, 07/04/1976, 1/1/2000
`

// Fabricated to match, never a revoked real key. The vendor prefix is the part
// under test; the body is filler.
const secretsSample = `=== Identifiants techniques ===
OpenAI : sk-proj-abcdefghijklmnopqrstuvwxyz0123456789ABCDEFGH, compte de service sk-svcacct-abcdefghijklmnopqrstuvwxyz0123456789ABCDEFGH, administration sk-admin-abcdefghijklmnopqrstuvwxyz0123456789ABCDEFGH, ancienne forme sk-abcdefghijklmnopqrstuvwxyz0123
Anthropic : sk-ant-api03-AbCdEfGhIjKlMnOpQrStUvWxYz0123456789
Google : AIzaabcdefghijklmnopqrstuvwxyzABCDEFGHI
AWS : AKIAIOSFODNN7EXAMPLE, temporaire ASIAY34FZKBOKMUTVV7A, porteur ABIAY34FZKBOKMUTVV7A, lié au contexte ACCAY34FZKBOKMUTVV7A, ancien A3TY34FZKBOKMUTVV7AB, et aws_secret_access_key = abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMN
GitHub : ghp_abcdefghijklmnopqrstuvwxyz0123456789, gho_0123456789abcdefghijklmnopqrstuvwxyz, portée fine github_pat_11ABCDEFG0abcdefghijkl_ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789AB
GitLab : glpat-abcdefghijklmnopqrst
Slack : xoxb-0123456789-a, applicatif xapp-0123456789-z, webhook https://hooks.slack.com/services/T00000000/B00000000/XXXXXXXXXXXXXXXXXXXXXXXX
Stripe : sk_live_abcdefghijklmnopqrstuvwx, sk_test_abcdefghijklmnopqrstuvwx, sk_prod_abcdefghijklmnopqrstuvwx, restreinte rk_live_abcdefghijklmnopqrstuvwx
SendGrid : SG.abcdefghijklmnopqrstuv.abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQ
Twilio : SK0123456789abcdef0123456789abcdef, en majuscules SKFEDCBA9876543210FEDCBA9876543210
Registres : npm_abcdefghijklmnopqrstuvwx, pypi-abcdefghijklmnopqrstuvwx, dckr_pat_abcdefghijklmnopqrstuvwx
Modèles : hf_abcdefghijklmnopqrstuvwx, r8_abcdefghijklmnopqrstuvwx
Inférence : gsk_abcdefghijklmnopqrstuvwxyz012345, xai-abcdefghijklmnopqrstuvwxyz012345
Clé privée collée en entier — le bloc est masqué corps compris, et il est écrit avant les mentions ci-dessous parce que sa fin est cherchée au plus court :
-----BEGIN RSA PRIVATE KEY-----
MIIEpQIBAAKCAQEAn6/O8li+SX4m98LLYt/PKSzEmQ++ZBD7Loh9P13f4yQ92EF3
yxR5MsXFu9PRsrYQA7/4UTPHiC4y2sAVCBg4C2yyBpUEtMQjyCESi6Y=
-----END RSA PRIVATE KEY-----
Clé privée seulement mentionnée : -----BEGIN OPENSSH PRIVATE KEY-----, bloc armé -----BEGIN PGP PRIVATE KEY BLOCK-----
JWT : eyJabcdefghijkl.eyJabcdefghijklmn.abcdefghijklmnopqrst, complété eyJabcdefghijkl==.eyJabcdefghijklmn==.abcdefghijklmnopqrst, indenté avant encodage ewogICJhbGciOiAiSFMyNTYiCn0.ewogICJzdWIiOiAiMTIzNCIKfQ.abcdefghijklmnopqrst
Base : postgres://admin:s3cr3t@db.example.com:5432/app
Broker : amqp://guest:gu3st@broker.internal:5672/
Cache : redis://:p4ssonly@redis.internal:6379
API partenaire : https://user:p4ss@api.partner.com/v1/orders
Mot de passe : PASSWORD=hunter2-correct-horse, entre guillemets password="hunter2-correct-horse)"
Nom portant le mot-clé ailleurs qu'à la fin : STRIPE_SECRET_KEY=f6CGV4aMM9zedoh3OUNbSakBymo7yplB, en camelCase accessTokenValue=Kymo7yplBf6CGV4aMM9zedoh3OUNbSa
Mot-clé mal orthographié — lettre doublée SUPER_SEECRET_VALUE=oh3OUNbSakBymo7yplBf6CGV4aMM9zed, lettres séparées S_E_C_R_E_T=UNbSakBymo7yplBf6CGV4aMM9zedoh3O
Nom "credential", et pas "key" — un "key" nu est ce que la moitié des langages de configuration appellent la partie gauche d'une paire : CREDENTIAL=zedoh3OUNbSakBymo7yplBf6CGV4aMM
Séparateurs de table associative : 'token' => 'Bymo7yplBf6CGV4aMM9zedoh3OUNbSa', et api_key -> akBymo7yplBf6CGV4aMM9zedoh3OUNb
Déclaration courte Go et affectation Makefile : apiKey := "9zedoh3OUNbSakBymo7yplBf6CGV4aMM", TOKEN ?= edoh3OUNbSakBymo7yplBf6CGV4aMM9z
Abréviation du nom, telle qu'un .env l'écrit : DB_CREDS=doh3OUNbSakBymo7yplBf6CGV4aMM9ze
Ponctuation dans la valeur : password="Wh4t?Really", sans guillemets PASSWORD=Wh4t?Really1, point-virgule password="a;b;c1234x", virgule password="red,blue1x", liste API_TOKENS=abc12345,def67890
Paire de délimiteurs appariée — de la ponctuation tapée, pas de la syntaxe : password="pa(ren)th1s", password="[brackets]1"
Lettres seules, sans chiffre : PASSWORD=correcthorse, le placeholder que personne ne change PASSWORD=changeme, six caractères PASSWORD=mcjrx4
Point dans la valeur, quand la chaîne ne nomme rien : secret=abcdef.ghijkl
Et de l'autre côté de la limite — ces trois lignes doivent ressortir intactes, c'est ce qui dit où passe la frontière : secret = config.password nomme l'endroit d'où le mot de passe est lu, token = user?.token2 est un accès optionnel, secret_level: string est un type

=== Identifiants de fournisseurs, second palier ===
Un préfixe par fournisseur, et chaque notation qu'il émet.
1Password : A3-UJZDE8-GXD6NCF10EP-F91DH-ODZDO-C9IS0, ops_eyJspNxnyVmihA/2O76UMFxFkM/R5Kjp1vRt+1fjORS/6ilI8ihN5KXSc7Tvo/hBKqFYY/kv5ZJr3J1TWDtkwtDDb+xHKas1VOqg6YYZYn9ZhyiA4uoRgnatmUdjAWtGSU8po+799NksnRH9ucAUsdMlHUvTCQCyEZDz/TddJ8HyS5SUkCnD8zRA9a9SkpXz9w3QlY7Zkuvqdt7s8Stqcbnr3yBdGBLEPH1qhT61qtc4xatws8phP9nhFyJfm
42 Intra : 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
Adobe : p8e-629be2u66mr26846p7q9m2i0hz2uep1e
age : AGE-SECRET-KEY-1DN8FHFSGAWXEL2W2ME46VK59HP4AUPC4JY8WX9S3ZT3GMSEFL593RTMY3P
Aikido : AIK_CI_oFzQFm2OEQ3HdAVja76R, AIK_SECRET_nIChtP8HKQDLM7ToThwNScgrLRWzBQCABugjMgeP7cGq0pbqfi14ZgTsNOVM14tu
Airtable : patOizwd1iaeOV4qB.fbc6313b8ff28a25441b305fa46c9201ac2ce6b6a331ce649e3eb7ea1cabe5d6
Alibaba Cloud : LTAI1cjDoBoirPfQAdzEv, STS.7g5iFqhEvveQzE2Q
Apify : apify_api_PuwNOvpdf2YEe6rSxCnopMEmJVQpvsTnkI
Artifactory : AKCpAeDfRrGsNrfSthSdddxH5jMTF7eBSdE0g9cRYN687NElFJvhQ8XIm0ogR4HtXOf54fZB, cmVmdKA8frcZTuJaWYUH1VAUwV1ZH87MtA5vSQXEZY3lEX7bwR2DRGD1qSo7JPRb
Asaas : $aact_prod_oYv2DzaKG05Rk_GQV81r, $aact_hmlg_kmghzem9yPVUJa-c5q52
Authress : sc_i9mpf.lv9f.acc-pxq-mb0y07.nyrvd5r+xi67-nfrpyz21tbic14/5a, ext_ez732.pgoj.acc_7g3f9caio-.cti-q7-1hget7/myqo_aa8t3rup47p, scauth_9pb0t.dbm5.acc-fqo1xo5cv0.xzmas6en5mtmo3oqsg=5=lo50d_jzd, authress_nbj0d.dlz2.acc-hfkvml73ct.yxv2kgafrfw0h9nywt1fd4mx82mux4
Azure App Configuration : Endpoint=https://example-config.azconfig.io;Id=abcd;Secret=abcdefghijklmnopqrstuvwxyz0123456789
Azure Service Bus : Endpoint=sb://uu3.servicebus.windows.net/;SharedAccessKeyName=P;SharedAccessKey=KZyUf0IE9pU2NJhKaM1/5WdR16ePllji
Brave Search : BSAvghZ4fXfeTkYpIygfdM7ENA8
Brevo : xkeysib-5C7Ef653Cc3be1c61D641ac6ed0Cd712Cc28Fdb3DAc8CCFA444168C28E093dbe-ZRsdM3IVV8iwO2y2
Buildkite : bkaa_d5vFldPGYYJvW5hANsbEvrSFagEaBp0vXnJaE-9I0MyTLUyi0kn1Gnt11CuZyzaA3U2OLzu6UQB, bkua_d9jzfx6kjwsk7kegy5mtic4udyfkozm4lncz7kyw, bkua_hqekxwqaflliz8x7f5qjrhow1n5946k2ruadyq1nj3e6vp749o96q
Canva : cnvcaptFyfePpX6N1NF2XV54wca_7E56w8ZniqT3Ul4ffqkOkgWrdioy
Cerebras : csk-i5skoewqkur3jq64nq6puxcmlzkruykqh7dx297gq8zxqyxj
CircleCI : CCIPAT_xvWfColNV9ds0HqtO93L7Q_uacojs106xdi5ocbdawtg7w8o0tinx4kiapj2gej
ClickHouse : 4b1d3qyRZzQ9ADp0j5Wmplcm7hufPK5ACDiBZLPKD6
Clojars : CLOJARS_ga9mj0m760l6tetd48ay13f2logqochvqdr917qsnf6akqpmkumyvpy8447a
Cloudflare : v1.0-a71306cfebaddf5eaabebcbc-50c6d100dbbc39ded034472a523b5493a7a7d59b0c3f7a03ba59d9f952f3019fdc9d45d66c7a50327f618eb54e84f8821e481023ee145f1402dfd06ee33720dd2068ba67138ae26a17
Cloudsmith : csa_711fd8742d716f2798a7f4a69db20fty88Mh
CockroachDB Cloud : CCDB1_WG2kdiNtegBoy1XhVav8dN_rLZgw7HunWoDQRYZDAEa6aosrWlQGOTvZ89hOz9Z
ConfigCat : configcat-sdk-1/7bVQIY8cSt07lQ8tdiwg2X/9Ajtfmp9_2KuTmxHKpRsBB
Databricks : dapi0c32de1f85e06fc3090c8dd271e99b98-2
DataStax Astra : AstraCS:sfPfKim3vAK1UdskfqS1
Deno Deploy : ddp_dXba9rELoXopBBnCrv7VzGgefw5JCNtaoIVG
DevCycle : dvc_client_3qXVexhj, dvc_mobile_x6NSbVbQ, dvc_server_jD0SSW0f
Devin : apk_user_zqisa/PqYomQLFzzGzmNAFY8HwSKbF6WMXE1MBvRnhmX1EoC3G/FP1z5IBxT80NK8bTB2ABPLbPQ8Cjf5XGuSKl/6gGEBHBKxnnV+Hov48VSOuU19x5iqljH, apk_qBTn2fwxwd5kAphi2UFkSSj/sK+wZdnHy7agBx6LtIdyhp9ZYbYLXlutzTfF/vNv7KToDsjCMEa+bhj2, cog_g4iqcvmlyfbdcx57ezhfquofzl4kxpolcqwdbdq6dgjuamt4g6ux
DigitalOcean : doo_v1_26d596f81ea80bf1c5e8d6ac84419d5e41bf8e8e2771ea234f29d489deb093d2, dop_v1_057211d637fb3ea84e8a3f57b702fef1f0cc92f0e030ac7b5439ca79e21f5bf5, dor_v1_a58cd5146b3d98aea1c1ffd32aad02a818d5dfb2d892ddd6e11e86fa67b6b546
Doppler : dp.pt.pv1uz9du7jwp1axg7leu1m6boi0z3cccrr8cgqh7a1p
Duffel : duffel_test_cshtwkhd=6rf3-8j2h6is0_srpf8s3_oym9x39t44tb
Dynatrace : dt0c01.pvom68yzawkpu9u5rsnsdbk9.ew2d7y2wg7oj0vwimr7g4ri0ga09h5zj0rhy23swswz79yua5y2tl8tj1yofvupu
EasyPost : EZAKn1abdq5t8t81771y3wcw2ae7og0x6z9jm05z2v7fkxuxet6lhsv60k, EZTK7s6n6m0ldgwc0aat9atzgabml59r86jm0hjk76gbgek7531daujpwr
Elastic Cloud : essu_VEiMIsY5xCGcyF4GefcFUWoA6m1g-Ifxc0nz_CfLWVtwXAlyuOqxqzIP2sfx
Exoscale : EXOmDswpBcrQbvZjpTifmrI1YiJ
Facebook : EAAC3pkxwnzynt46no2iq2x8pz6nih6f8rybjtayfloumge9x6tmetfosizswz3irlbxw0b3pzwglshroczck1mtjyc9tlo57q1wahsc
Figma : figd_-DPHCUNWF0ZOR7FW12V626DN16I5MC9QL8KP8Q
Flutterwave : FLWSECK_TEST-hbf335cg1ee7, FLWSECK_TEST-7hha86e31eeh2d95fe64gd1a37gbb01g-X
Fly.io : FlyV1 n5OUp47ulVJFB7=KqhN=3=YpBtLkgfKRDDySlvX+VNnpwXtodvRvgeHFNzGb_2_UmKSdUR4zLF49YbvAE,2SkJH,1rI4BWVwlA4s
Frame.io : fio-u--m4f8u7318jz=fdv=t--0x4itv7bmo2fj_x9_0x7p-2zqholm9hoqgm7q5o93o8-
GC Notify : ApiKey-v1 gcntfy-ShV-2d2e433e-c56f-24b1-c71b-106e934d263b-5ba0837b-bf1b-3ba3-178b-6e0e30f32854
Google Gemini : AQ.Ab8RN6lwSgi4BDrT_9EEJXy8U5ydJuqbnQFbVu7q7xtoAq9qdC
Grafana : eyJrIjoif6FSSixiIhtREMZ2MukeSJmrufszqHrp9vfesTRa, glc_A6z5ymVISmngrJYKWmt7t2I+oWjgCVieCbGz5ZkM, glsa_MPuD9ImDFEz04kVuIAMRip4AoU7BNUU3_A83079eF
Harness : pat.h1flQ-ZG7bdOOh1QulctAs.2bbdb4a78f19e8b8480f3b47.zWf7bNihdIGnJXlq8MxV, sat.twudSF4-BSX6BPdnbiZShD.cdc70808d77b6ad89f65f849.sfvaF35pkuRNM9CnLd4Y
HashiCorp Vault : hvb.7S_dTZAuS-Zut2x8AzFTmHJSp9KWBO3aMGrqvLm3733ymt0wtOC3XJtmxyu8y4_mcz4en3BNDwSVn9iuNtGmhgzFAkGGlH_xGaM7CVF0oCboQn5_cCASeOX0YCN1j438Jw00BgB7Fp, hvs.kV3bbH_uy8qM3AsYaLcW4PDRiqgkKfLNuoliMdVwY1pp7M_4Xn3DWzP9WYJof5Hzt4XJUtv2tIEpc1ke4M4innZMcW
Heroku : HRKU-AAqK5UnThC3ej1hCjJclXObRHOG4up14htTd26E8ef_hS0msieJ-9Irs9ym1
Infracost : ico-qre9cmGdAYJ8xrauScPDIsJvSA3VTrzB
Ionic : ion_GXWqzhhTcqFRZScsHcoeuzLwhJArIXfhqPnXhVzYQB
LangSmith : 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, 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
Lichess : lip_SjVxYxdHFO2Ek0AG
Linear : lin_api_5fn3dmv4d90i0djuvm7al8r7qfuyqt9z60dttpy1
MailerSend : mlsn.2iQTMIDNipX7dqftlJX7zVMd6tjqDu
Mercury : mercury_production_kar_Ea8k0UCROycSMtNzlndZ7ucN4NDLb2oHDI34E0mf_yrucrem
Mergify : mergify_application_key_XBV-clbUSaM7MZLG1cg42THRFU5ldoTnhpbTdyEp
Microsoft Teams : https://example.webhook.office.com/webhookb2/abcdef01-abcd-abcd-abcd-abcdef012345@abcdef01-abcd-abcd-abcd-abcdef012345/IncomingWebhook/abcdef0123456789abcdef0123456789/abcdef01-abcd-abcd-abcd-abcdef012345
MiniMax : sk-api-ZkzaQeeMBNG_adLVThD2yOlPKbdfHfJrMFbWmrK7XBo00ELfSVTsRaZcqIA9E-qIIZGu0LsU--RhmG7V3xmOIgdeZ6e-GyyrwzLdr2nAm_CO810m6SqbKty
MongoDB Atlas : mdb_sa_sk_7ElqLiX40ePbFwXxiqTuVcsyn-oYUyBAWNf6gtMw
Neon : napi_I7w5QqaEgnVcR9SXTqtorY8hzrD6pffXsBD414rHjYcTwg5JumvdC8UeIA875RJM
Notion : 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
NVIDIA : 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
Octopus Deploy : API-ZZVZZ5VWLJ870SINVE0E6AP1ZN
OneSignal : os_v2_app_rijophscysiyrernotgxfxbehuna5i4rd4cc5h6osvvonnsbolbr3xerfhzy2odxvqe6i355mvmhzksmeb4mmqmsbbewn2aqwkuwtgc
Onfido : api_live_ca.Wt1D6NrNTu8_Kro8QNgx, api_live.atgCYj3xU3RRBObwDBL7, api_live_us.FaJpr7_aAfatwNMQZ464
OpenRouter : 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
OpenShift : sha256~lTmlEmlVJMNLs-QyakjfoBX60Akchdr3hxL4GrGMSdP
Paddle : pdl_live_apikey_ygk2k4urpa08bvo8wvapvf8kgc_02UboVXEiH9dKNhDpqiP86_a76
Perplexity : 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
Persona : persona_production_-jeezteee8aexej9h56r
Pinecone : pcsk_6xcL5_GQTZassLcu4G37dVU1NBY1yOG2NzWqVRnA2ME5FKyqqlTqQLCJeG1DYQpFklODE
Pinterest : pina_lBiQtuWRvgvuVOfVkwDc
PlanetScale : pscale_tkn_yCXUE8HagmWVEKd84_oo6_lZp_9wD24h, pscale_oauth_pyiIU48ERhj-C9BWoh3hEv-OBmk9H76q, pscale_pw_j5OmAJUip89Gx-b-d8eD=rUsXPfVxDc6
Polar : polar_at_K5bEk4RYmoZIzDVBu9dI, polar_oat_9v_bbY8Zn6icpE0Wr0Cv, polar_pat_UeATh68xRhePj1TRRpHV
PostHog : phx_D2vk50GCtI0mg3ncLjKwr1jWMo5F-Vy3jGWxGE0UG, phc_jh8BPb48Rx7PD3lA0ZrDVUW-UqCBIoerZ1j86QTS3
Postman : PMAK-4f9af65d3010532fc8b0a72a-cafc1af1f21aadcc0e94c5437924bc2f2c
Prefect : pnu_eNdSqiY3UvvGFjmM7JZdWj1SBysTbotZeZEg
Proof : prf_cli_ITY57dL83RBYbN6eh2qH
Pulumi : pul-a1a13080f032efb1843643b4c3b41ef18a04d593
Ramp : ramp_sec_xEGqEnYbeEQzqgOcU2e8taxtXicx7u7UnDGxdFo7RIC286jI
ReadMe : rdme_e3ctev17fjzgdcsi7geuk80kply1vxhp39hfqy4ols3zmim5g6vpbq64juulvm0daowaqc
redirect.pizza : rpa_5C8UO2U04R8XTXnWZYSH8OA6rawox4
Render : rnd_kw6P06pzD4uKwJ0TQgpUYb1TIPit
Rootly : rootly_4b5f4eb84980451cdd4aa15cc9b086396394535dc987a10055db87ae7cf35d1b
RubyGems : rubygems_157f6c70434f9ae6ffad5bb0a08e0ee8a7e221708bca4f12
RunPod : rpa_O7LOLMH3NR16D5A2FE90JU3KN8V0PMOK0W1TTKN2FJMlUH
Salesforce : 00gSLae1cxlfe8R!8Z8S-VdJtxIzMt2qtyT7AF9tz3mUASuzpcrUzXkORDp94_juCsp9OqgxhCvxIuBjqk_UwCJYaHRSndcH
Samsara : samsara_api_bQHuu66G8Jjj7Fx7Jb1MCvf2uY
Scalingo : tk-us-2lwqMekhupecPvo7unxzTzUp3PY0G5D9dwvxtSh5e4b54cRY
Segment : sgp_g8J3D6yjhJfLsYKspAgz7ysg8A2zXatqMkYuqaV9e9l7nKU5YMR5Nyqyn0AlsUUp
Sentry : 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, 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
SettleMint : sm_aat_eM1SBh1V5rGjBx3Q, sm_pat_b9bdBNIPykxUxJiw, sm_sat_65xqIjkkjjhLYZhk
Shippo : shippo_live_D466d53125abB1eBaBFBc3601E3bB9b24Bb7fAcC
Shopify : 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, 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, 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, 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
Sourcegraph : sgp_aec51B8e9Cdd0c9B_aebFcD6E562865AD4A3EeFF456B7C94e4a1197fb
Square : EAAALJp5V8FWLLZeG9PB5TN6Ul, sq0atp-UAD3GUcIhRU0e3NDRR8nx_
Supabase : sbp_gxmr5civ02s0jujlkwrdpvcld11mjx6hhr26zqbz, sb_secret_xXwBvOpqQEYaCdlMZed8pPEpL6Peb4n
Tailscale : tskey-api-1uBdOqze2fqewEmi897B
Temporal Cloud : eyJGw7dW8xUNh.4LnY2NvdW50X2lk7bAInRlbXBvcmFsLmlvILLICJrZXlfaWQiOiXvA306lsvVM-Ovlacxtq.jkKvOupRqOrU1CuczAUZ
Thunderstore : tss_5uzhdW6VvHDwcpzF-8ZW
Together AI : tgp_v1_IWXhRVolR9ORjnmZc4oQu-5VHNKESiIWCCd4L6eXZor
Unkey : unkey_mBIVXE6EBnuHDKsSqRT6
UpCloud : ucat_lv5tDzScoHZx0p3kIEJ5yxgZ
Val Town : vtwn_9Sw7w6ZcjifRnyFcMb4v
Vercel : vck_uQ8LCDTcKLYJRl14geoGM0nHOM2Ibj-lX3Ck6pmjKM-rdvOolnvf0je3, vca_7gaRQBKgWuhYz7WMmNX81FYyy2ZvkzzyYxSr7EKeJWui68qnvXWVLTb9, vcr_rNTScqkmKiayB3cw7B4wAMdzgeDM71Lf5kbHvEPC_SzT7iszUYLq3Ylp, vci_GvNEqghj35577oOWOfQaRa-qYq59FWHW5JI5DC90L0dRG0ern_1yHBpE, vcp_3ZcqBDMH2_-vMwoBxh0I-wN_MzN-3DO8mF1jA8fs7wNlGqnezD36S9mF
WakaTime : waka_sajudpbkqpyo7ujgp27ywj2l9sxb7r5dhkaz
Weights & Biases : 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
Workato : wrkafe-eyJvTfCPZnA.npMk7U4NLszXUaJA.LzKQf6G05ODyrZe3s6uQxIl1klPb3p4kY9mwLP5I42g-hyNdU3YA9wrwPKyTn0Qk
Zuplo : zpka_u23s4ilq6b0br85xn1b30mffotym0x31_bc37293e
Valeur entourée d'espaces dans ses guillemets : API_KEY=" MM9zedoh3OUNbSakBymo7yplBf6CGV4 "
Élément XML : <apiSecret>ymo7yplBf6CGV4aMM9zedoh3OUNbSak</apiSecret>
Entête HTTP : Authorization: Bearer 4aMM9zedoh3OUNbSakBymo7yplBf6CGV, et Authorization: Basic YWRtaW46aHVudGVyMg==
Clé hexadécimale : KEY=0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef
`

// Sample assembles what a deployment serves: the locale-independent reference,
// each selected locale's own, and the credentials.
//
// The locale sections come in registry load order, so the document reads the way
// the catalogue is applied.
func Sample(locales []string) string {
	wanted := make(map[string]bool, len(locales))
	for _, code := range locales {
		wanted[code] = true
	}

	var b strings.Builder
	b.WriteString(internationalSample)
	for _, l := range Locales() {
		if wanted[l.Code] && l.Sample != "" {
			b.WriteString("\n")
			b.WriteString(l.Sample)
		}
	}
	b.WriteString("\n")
	b.WriteString(secretsSample)
	return b.String()
}

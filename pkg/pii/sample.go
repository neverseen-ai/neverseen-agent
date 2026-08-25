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
IBAN : FR1420041010050500013M02606, groupé FR14 2004 1010 0505 0001 3M02 606
IBAN étrangers : DE89370400440532013000, BE68539007547034, NL91ABNA0417164300
Serveur : 192.168.13.42, et 10.0.0.1 en secours
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
Code postal seul : 13290 Aix Les Milles
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
`

const unitedStatesSample = `=== United States ===
Social security: 123-45-6789
Employer id: 12-3456789
Routing number: 021000021, also 011000015
Telephone: (555) 234-5678, 555-234-5678, 5552345678, +1 555 234 5678
Address: 123 Main St, Springfield, IL 62704
Address without a city: 456 Oak Avenue
ZIP: IL 62704, and 62704-1234

=== Dates (month first) ===
THE SAME DATE in its three notations: 03/14/1987, 03-14-1987, 03.14.1987
With and without a leading zero: 3/14/1987, 03/14/1987
Other dates: 12/25/2024, 07/04/1976, 1/1/2000
`

// Fabricated to match, never a revoked real key. The vendor prefix is the part
// under test; the body is filler.
const secretsSample = `=== Identifiants techniques ===
OpenAI : sk-proj-abcdefghijklmnopqrstuvwxyz0123456789ABCDEFGH, ancienne forme sk-abcdefghijklmnopqrstuvwxyz0123
Anthropic : sk-ant-api03-AbCdEfGhIjKlMnOpQrStUvWxYz0123456789
Google : AIzaabcdefghijklmnopqrstuvwxyzABCDEFGHI
AWS : AKIAIOSFODNN7EXAMPLE, et aws_secret_access_key = abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMN
GitHub : ghp_abcdefghijklmnopqrstuvwxyz0123456789, gho_0123456789abcdefghijklmnopqrstuvwxyz
GitLab : glpat-abcdefghijklmnopqrst
Slack : xoxb-0123456789-a, applicatif xapp-0123456789-z
Stripe : sk_live_abcdefghijklmnopqrstuvwx
SendGrid : SG.abcdefghijklmnopqrstuv.abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQ
Twilio : SK0123456789abcdef0123456789abcdef
Registres : npm_abcdefghijklmnopqrstuvwx, pypi-abcdefghijklmnopqrstuvwx, dckr_pat_abcdefghijklmnopqrstuvwx
Modèles : hf_abcdefghijklmnopqrstuvwx, r8_abcdefghijklmnopqrstuvwx
Clé privée : -----BEGIN OPENSSH PRIVATE KEY-----
JWT : eyJabcdefghijkl.eyJabcdefghijklmn.abcdefghijklmnopqrst
Base : postgres://admin:s3cr3t@db.example.com:5432/app
Broker : amqp://guest:gu3st@broker.internal:5672/
Cache : redis://:p4ssonly@redis.internal:6379
API partenaire : https://user:p4ss@api.partner.com/v1/orders
Mot de passe : PASSWORD=hunter2-correct-horse
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

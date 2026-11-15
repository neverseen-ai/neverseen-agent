// The texts the Try it screen starts from. Each is made to show one thing the agent
// does, and each was checked against the real detector: what this page highlights is
// what `neverseen scan` reports for the same text.
//
// Fake values throughout, on a domain that is not a documentation one: pii leaves
// example.com, .example and .test in clear, so a sample using them would show an
// address going out that the reader expected to see masked.

// Built rather than written out, so no credential-shaped literal sits in the source.
const fakeKey = (head: string, length: number) => head + "MOCK".repeat(Math.ceil(length / 4)).slice(0, length);

export const SAMPLES: { name: string; text: string }[] = [
  {
    name: "Support ticket",
    text: [
      "Hello, I am writing on behalf of Claire Martin about invoice 2026-0412.",
      "She can be reached at claire.martin@halden-studio.fr or on 06 12 34 56 78,",
      "and her refund should go to FR76 3000 6000 0112 3456 7890 189.",
      "Her social security number is 2 84 05 75 112 245 31: please keep it out of the CRM.",
      "Delivery address: 18 rue des Lilas, 69003 Lyon. Born 14/03/1984.",
    ].join("\n"),
  },
  {
    // One customer twice: the same value gets the same name wherever it appears, which
    // is what lets the model reason about rows it cannot read. Names are not a category.
    name: "Customer export",
    text: [
      "customer_id,name,email,phone,birth_date,postcode",
      "6543fa1c9e8b2d0a4f1c7e93,Léa Bernard,lea.bernard@halden-studio.fr,06 12 34 56 78,14/03/1984,69003 Lyon",
      "6543fa1c9e8b2d0a4f1c7e94,Hugo Petit,hugo.petit@halden-studio.fr,07 98 76 54 32,02/11/1990,75011 Paris",
      "6543fa1c9e8b2d0a4f1c7e95,Léa Bernard,lea.bernard@halden-studio.fr,06 12 34 56 78,14/03/1984,69003 Lyon",
      "Why does the third row repeat the first one?",
    ].join("\n"),
  },
  {
    // Every value here belongs to gb or us: unload either and its lines go out as written.
    name: "UK and US customers",
    text: [
      "From: Oliver Hughes <oliver.hughes@halden-studio.fr>",
      "Hi, I have moved and my new postcode is NW1 6XE. My number is now 07700 900461,",
      "and my National Insurance number is AB 12 34 56 C. Could you update my payroll record?",
      "",
      "Forwarded for the US office: Emily Carter, SSN 512-34-8876, phone (415) 555-0132.",
    ].join("\n"),
  },
  {
    // Debugging a call that fails: a bearer token, a vendor key, and values inside JSON.
    name: "API call",
    text: [
      "curl -X POST https://api.halden-studio.fr/v1/refunds \\",
      `  -H "Authorization: Bearer ${"eyJ" + "hbGciOiJIUzI1NiJ9"}.${"eyJ" + "zdWIiOiJ1c2VyXzg0MjEiLCJleHAiOjE3NjcyMjU2MDB9"}.${"Mock" + "SignatureMockSignature42"}" \\`,
      '  -H "Content-Type: application/json" \\',
      `  -d '{"email": "lea.bernard@halden-studio.fr", "phone": "+33 6 12 34 56 78", "card": "4242 4242 4242 4242", "amount": 4900}'`,
      "",
      `{"error": "stripe_auth_failed", "detail": "Invalid API key provided: ${fakeKey("sk_" + "live_", 24)}"}`,
    ].join("\n"),
  },
  {
    name: ".env file",
    text: [
      `DATABASE_URL=postgres://app:${"Mock" + "-Pass-" + "42"}@db.internal:5432/orders`,
      `ANTHROPIC_API_KEY=${fakeKey("sk-ant-api03-", 40)}`,
      `GITHUB_TOKEN=${fakeKey("ghp_", 36)}`,
      `AWS_ACCESS_KEY_ID=AKIA${"MOCK".repeat(4)}`,
      `SESSION_SECRET=${"q8Rz" + "Mock" + "Xv2Lp9" + "Tk4w"}`,
      "ADMIN_EMAIL=ops@halden-studio.fr",
    ].join("\n"),
  },
  {
    // What a review through the agent looks like: the password written in clear is
    // replaced, the code that merely names one is left as it is (GenericSecretCheck).
    name: "Code review",
    text: [
      "// src/payments/config.ts: please review",
      "export const config = {",
      `  db${"Pass" + "word"}: get${"Pass" + "word"}(env),`,
      "  stripeSecret: process.env.STRIPE_SECRET,",
      "};",
      "",
      `const db_${"pass" + "word"} = "${"Hx9!" + "mQ2v-Lr7"}"; // TODO: move this to the vault`,
      `user.${"pass" + "word"} = req.body.${"pass" + "word"};`,
      'const supportEmail = "support@halden-studio.fr";',
    ].join("\n"),
  },
  {
    name: "Server log",
    text: [
      "2026-11-08T09:14:02Z WARN login failed user=marc.dubois@halden-studio.fr ip=10.4.2.17",
      "2026-11-08T09:14:05Z INFO session 6543fa1c9e8b2d0a4f1c7e93 resumed from 2a01:cb19:4::42",
      "2026-11-08T09:15:40Z ERROR payment declined card=4111 1111 1111 1111 retry=1",
      "2026-11-08T09:15:41Z INFO callback to +44 7700 900123 queued",
    ].join("\n"),
  },
];

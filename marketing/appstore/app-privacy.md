# The App Privacy label

App Store Connect's App Privacy questionnaire is answered by hand (deliver can't upload it). This is what to
answer, and why, so the label and the app change together: when the iPhone app starts sending anything new
anywhere, update this file and the label.

**Answer:**

| Question | Answer |
|---|---|
| Do you or your third-party partners collect data from this app? | Yes |
| Data types | Identifiers → Device ID, and nothing else |
| Purpose | App Functionality |
| Linked to the user's identity? | No |
| Used for tracking? | No |

The listing then shows "Data Not Linked to You: Identifiers".

What the iPhone app sends, and where:

- **To the person's own Mac (or server running Hammock), directly**: everything it shows and does. That's their
  own computer, over their own network or VPN, not the developer's; Apple's "collect" means sent off the device to
  the developer or a third party, so it isn't collection.
- **To the push relay** (`relay/push`, run by Homegrown Software): when the person turns on notifications, the
  phone registers its APNs device token there, under a random ID its Macs send to. The relay keeps that token, the
  random ID, a SHA-256 of a secret the phone holds (to let it unregister), whether it's a development build, which
  push service it's for, and when it was registered and last used, until it's gone unused for 60 days. That's the
  Device ID: Apple counts data as collected when it's kept "longer than necessary to service the transmitted
  request in real time", and an APNs token is a device-level ID. The optional-disclosure exemption doesn't fit,
  since notifications are a main feature rather than an occasional one. It's used only to deliver the
  notifications the person asked for (App Functionality), isn't tied to an account or identity (there is none),
  and isn't used for tracking. Nothing else about the person, and no content: notifications are encrypted on the
  Mac for that phone (a key only the phone and its Macs hold) and the relay only passes them to Apple. The relay
  reads the client's IP address only to rate-limit, in memory; it isn't stored or logged (Workers Logs are off in
  `wrangler.toml`).
- **Nothing else**: no analytics, crash reporting, advertising or tracking SDKs, and no account. Crash reports and
  TestFlight feedback that Apple gathers are Apple's collection, not the app's, so they aren't on the label.

If the relay ever kept more (an IP address in a log, say, or turning on Workers Logs), or the app added crash
reporting, the label would need more (**Identifiers → User ID** or **Diagnostics**): revisit this then.

The privacy policy the listing links to (`privacy_url.txt`) says the same, in the website's words.

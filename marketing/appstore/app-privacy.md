# The App Privacy label

App Store Connect's App Privacy questionnaire is answered by hand (deliver can't upload it). This is what to
answer, and why, so the label and the app change together: when the iPhone app starts sending anything new
anywhere, update this file and the label.

**Answer: Data Not Collected.**

What the iPhone app sends, and where:

- **To the person's own Mac (or server running Hammock), directly**: everything it shows and does. That's their
  own computer, over their own network or VPN, not the developer's; Apple's "collect" means sent off the device to
  the developer or a third party, so it isn't collection.
- **To the push relay** (`relay/push`, run by Homegrown Software, so this is the one to watch): when the person
  turns on notifications, the phone registers its APNs device token there, under a random ID its Macs send to.
  The relay keeps that token, the random ID, a SHA-256 of a secret the phone holds (to let it unregister), whether
  it's a development build, and when it was registered and last used. Nothing about the person, and no content:
  notifications are encrypted on the Mac for that phone (a key only the phone and its Macs hold) and the relay only
  passes them to Apple. The token is used only to deliver the notifications the person asked for, not linked to an
  identity, and not used for tracking, so Apple's definitions leave it out of the label. Devices unused for a while
  are deleted (`relay/push`).
- **Nothing else**: no analytics, crash reporting, advertising or tracking SDKs, and no account.

If the relay ever kept more (an IP address in a log kept longer than it takes to deliver, say), or the app added
crash reporting, the label would need **Identifiers** or **Diagnostics**: revisit this then.

The privacy policy the listing links to (`privacy_url.txt`) says the same, in the website's words.

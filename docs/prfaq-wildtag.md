# WildTag — PR/FAQ

**Author:** Dylan Murray · **Date:** 1 September 2026 · **Version:** v0.1 · **Status:** Draft
**Reviewers so far:** none

> **Draft notice.** The BSVA quote below is a placeholder pending a real attribution. The
> customer quote is illustrative and invented by role, as the template instructs. SCDNR is
> named because the system is built around their published material — **they have not been
> contacted.** See internal FAQ 2.

---

## Part 1: Press Release

### Wildlife agencies can pay a tag reward in seconds — and get the recapture position from the boat instead of from memory

**For biologists running tag-and-release programmes, and for the anglers and crabbers who
catch the tagged animals: a printed code that pays the finder on the spot and records where
the animal actually was.**

**Charleston, South Carolina** — A tag on a released fish carries a phone number. Somebody
catches the fish, and mostly they do not call. When they do, a reward turns up weeks later,
often to the boat captain rather than the person holding the fish, and the position of the
catch is whatever they remember by the time a form reaches them. WildTag replaces the phone
number with a code the finder scans: the money is in their phone before they have put the rod
down, and the position is taken there, on the water. Both are written into the same
transaction, so the agency's record of a catch and its payment for that catch are one object
and cannot come apart.

**The problem.** SCDNR's own review,
[*Don't Underestimate the Reward*](https://www.dnr.sc.gov/marine/tagfish/pdf/Sept13-Don't%20Underestimate%20the%20Reward.pdf),
reports that fewer than half of encountered tags are ever reported, and that tags carrying
money do better than tags that do not. The fisheries literature blames delivery rather than
generosity: the finder's identity is lost when a captain collects the tag, weeks pass, and a
prize draw arrives too late to be an incentive. The data suffers the same way round —
precise release positions recorded by a biologist, recapture positions remembered days
later. And the one thing reporters say they want, the animal's own history, nobody sends.

**The solution.** The tag stock does not change: same plastic, wired on the same way, with a
code added and the phone number left where it is. A biologist arms a tag from a phone, in the
field, with the animal in hand. Whoever catches it later scans it, is shown where the animal
was tagged and how far it has come, answers four questions, and takes a position fix where
they are standing. The reward arrives in seconds, into a wallet the app made for them the
first time they opened it. No account, no form to post, nothing to reconcile afterwards.

**Quote from BSVA.** *"[Placeholder — attribution to be confirmed.] Small payments to people
who have no account with you, made at the moment they earn them, is a thing our network can do
and most cannot. A wildlife agency paying a dollar to a stranger on a boat is a better first
demonstration of that than any pilot we could invent."*

**How to get started.** Order one batch of tags with codes printed on them, fund a wallet with
the reward budget for that batch, and hand the tags to whoever is already doing the tagging.
Nothing else in the programme changes, and every tag already in the water keeps working
exactly as it does now.

**Quote from a customer** *(illustrative — see draft notice).* *"We hear back on fewer than
half the tags people actually catch, and I have had anglers tell me they did not call because
they did not want a T-shirt,"* says the marine resources biologist who runs an inshore tagging
programme. *"What I care about is the position. Right now I get one somebody remembered three
days later. This gets me one taken on the boat, with the fish in their hand, and I get it
because paying them and recording it are the same action."*

---

## Part 2: External FAQ

**1. What exactly is this and who is it for?**
A tagging programme's reward system, replaced. It is for the agency that runs the programme
and for the members of the public who catch tagged animals. The agency gets more reports and
better recapture positions; the finder gets paid immediately instead of eventually. The
software is a server the agency runs, a public web page, and a phone app for Android and iOS.

**2. What does it cost, and who pays for what?**
The agency sets the reward per tag and funds it — nothing is charged per report. Sending the
payment and writing the record costs about 250 satoshis for a tag's whole life, which is a
fraction of a cent and roughly one per cent of a one-dollar reward. Compare that with a cheque,
which costs an agency several dollars once handling is counted, or a card payment at roughly
thirty cents plus a percentage. Those numbers are why a one-dollar reward has never been
practical to send. The software itself is open source.

**3. How is this different from what we do today?**
Today is a phone number and swag, and the honest alternative to consider alongside it is doing
nothing. The differences that matter: the finder is paid at the moment they report rather than
weeks later; the position comes from the device at the catch rather than from memory; and the
dataset is written into the payments themselves, so it can be rebuilt from public records if
every server involved disappears. The parts that are not different: the tag, the field
protocol, the species rules, and the phone number, which stays printed on the tag so anyone who
would have called still can.

**4. What do I need before I can use it?**
Tag stock with a code printed on it, a wallet funded with the reward budget, and a server —
one binary and a database, on a laptop or in a cluster. Finders need a phone with the app; it
makes them a wallet the first time they open it, with no account and no sign-up. Taggers sign
in; anglers do not.

**5. Who supports it when something breaks?**
Open for a production deployment. In a pilot, the authors. Beyond a pilot this needs a named
integrator with a support agreement, and the agency should not sign a pilot without knowing who
that would be. See internal FAQ 4 on why we think the reference implementation belongs with a
non-profit and the support relationship does not.

**6. What are the regulatory and compliance implications for me as an agency?**
Three, and none of them is settled. **Public records:** a report includes a citizen's position
and their wallet's public identifier, and it is published permanently. Whether that is
compatible with your state's records and privacy law is a question for your counsel, and it is
the first question we would want asked. **Procurement and treasury:** the agency has to hold
and account for the reward budget in satoshis, which is likely a harder internal conversation
than the technology. **Payments:** an agency paying its own posted reward is settling its own
obligation rather than transmitting money for others, but that reading needs your counsel's
agreement, not ours.

**7. What is BSVA's long-term commitment?**
The implementation is open source, and the record format is intended to be published as an open
specification so a second implementation is possible without us. If BSVA stopped work tomorrow,
an agency running it would keep running it: the server is a single binary with no licence check
and no service dependency on BSVA, and the dataset is already public. What would be lost is
maintenance, not operation. We think that is the right shape of commitment to offer a public
body, and it is deliberately less than "we will look after you forever".

---

## Part 3: Internal FAQ

### Customer and market

**1. Who precisely is the first customer?**
The marine resources biologist who runs SCDNR's inshore tagging programme in Charleston. The
first three users would be that biologist, one authorised volunteer tagger — the marine game
fish programme runs substantially on volunteers rather than staff — and one angler who catches
a tagged red drum.

**2. How do we know they want this? [OPEN — the weakest part of this document]**
We do not. **No agency has been contacted and no conversations have been held.** The evidence
is entirely documentary: SCDNR published the reward problem themselves, in their own review,
and concluded that tags carrying money are reported better. That is a real finding by the real
customer about the real problem, which is more than most proposals start with — and it is still
not the same as somebody saying they would use this. Anyone reading this document should treat
the demand case as unvalidated. The cheapest next step is not engineering; it is one
conversation with that biologist, and it should happen before anything else in this document is
funded.

**3. How big is this if it works?**
SCDNR's marine game fish programme alone covers
[46 species across 20 families](https://www.dnr.sc.gov/marine/tagfish/tagfish.html). Every
coastal and inland state runs programmes of the same shape, as do federal agencies and
universities. Beyond tagging, the same pattern — a bearer instrument, a claim that can be made
exactly once, and a timestamped field record — is what harvest tagging is: South Carolina
already mandates electronic deer reporting through [SC Game Check](https://dnr.sc.gov/harvestreport/),
which issues a confirmation number a meat processor has to trust a database to validate. That
is a materially larger programme than fish tagging and a natural second act. It is deliberately
not in v1.

**4. Why is BSVA the right party to do this, rather than an ecosystem company or nobody?**
Two reasons and one tension. A state agency will not take a dependency on a startup that may
not exist in three years; a non-profit steward with a published maintenance position is a
different procurement conversation. And the output is a public dataset and an open record
format, which is standards work rather than product work.

The tension is real: BSVA maintaining a reference implementation edges toward competing with
the integrators we exist to enable. The split we would propose is that BSVA owns the open
implementation and the record specification, and integrators own deployment, hosting and
support contracts with agencies. If management does not accept that split, this should be
handed to an ecosystem company with BSVA contributing only the specification — that is a
legitimate outcome of this review, not a failure of it.

### Product and technology

**5. What has to be true technically for this to work?**

| Assumption | Confidence |
|---|---|
| Transaction fees stay far below the reward | **High.** Measured at 98–143 satoshis per transaction on TTN today. |
| A member of the public can hold a wallet without understanding it | **Medium.** The app makes one silently on first launch and it works, but no member of the public has used it. |
| Arcade/Teranode availability adequate for a field application | **Medium.** Adequate in testing; propagation lag was visible during development and is documented. |
| Mobile wallet stack is dependable | **Low–medium.** The storage provider is copied from bsv-browser, not a supported dependency. See question 8. |

**6. What is the hardest technical problem, and do we know how to solve it?**
It is not the chain, and that is the uncomfortable part. The hardest problem is that a public
agency must acquire, hold, account for and audit a balance in satoshis. That touches
procurement, treasury policy and the state auditor, and we do not know how to solve it. Every
technical problem in this project has been solved; this one has not been started.

The second hardest is the finder's exit: an angler paid a dollar in satoshis has a dollar they
may not know how to use. We have deliberately not built a cash-out path, and a pilot that
ignores this will produce a good reporting rate and a bad experience.

**7. What do we explicitly not build in v1?**
The harvest/deer workflow — it is modelled in the data model so the abstraction was shaped
against real difference, but it is not built. Multi-state or multi-agency deployment. Any
fiat cash-out path. Tag printing hardware or stock supply. Any token: the rewards are
ordinary satoshis, and introducing an instrument would change the regulatory conversation
entirely for no benefit.

**8. What existing BSVA work does this depend on, extend or conflict with?**
Depends on Teranode and arcade, which it runs against today, and on the BRC-100 wallet
interface, BRC-29 payments and the BRC-48 PushDrop output shape. It extends none of them and
conflicts with none. It could contribute one thing back: the record format — a signed field
observation with a species profile that is data rather than code — is a reasonable candidate
for a BRC, and no equivalent exists.

One dependency is weaker than it looks. There is no published mobile storage provider for
`@bsv/wallet-toolbox-mobile`; ours is copied from bsv-browser with attribution, and it tracks
an abstract base class that can change underneath it. If BSVA wants field applications on
phones at all, that gap is worth closing properly, and this project is a reason to.

### Regulatory and legal

**9. What is the regulatory exposure, for BSVA and for the customer?**
For BSVA, publishing open-source software and a specification: low. For the agency, three
exposures, in the order we would worry about them.

**Public records and privacy — the sharpest, and the one most likely to be missed.** A report
publishes a citizen's position, the time they were there, and a public key that is stable
across everything they ever report. That is a permanent, public, un-deletable record about a
member of the public, created by a state agency. State records law, any applicable privacy
statute, and the plain question of whether the angler understood what they agreed to all
apply. **[OPEN]** — needs counsel, and we would not run a pilot without an answer.

**Money transmission.** An agency paying its own posted reward is settling its own obligation,
not transmitting funds for a third party, which we believe puts it outside transmitter
licensing. **[OPEN]** — our belief is not advice, and this needs a US-qualified opinion.

**Procurement.** Acquiring and holding satoshis against a budget line. See question 6.

**10. Are there IP, licensing or patent considerations?**
The implementation is ours to license openly. One third-party dependency is vendored rather
than depended on and is attributed in the source. The mapping tiles used by the public page
come from OpenStreetMap under their usage policy, which a real deployment should replace with
its own tile server rather than lean on donated infrastructure. No patent search has been done.
**[OPEN]**

### Cost and execution

**11. What does this cost to build and to run?**
The system exists. What remains before a pilot is roughly **three engineer-months**: agency
onboarding and operator documentation, a decision and implementation for the finder's cash-out
path, hardening the mobile wallet dependency, and a support runbook. Add **two to four weeks of
legal and policy time** for questions 9 and 6, which should start first because they can kill
it. Running it costs almost nothing: one binary, one database, and the reward budget the
agency chooses. Ongoing maintenance is perhaps a quarter of an engineer.

**12. Who would build it, and what do they stop doing?**
It is one engineer who already has the whole system in their head, so the opportunity cost is
whatever that person is otherwise assigned to for a quarter — management should name it
explicitly rather than assume it is free. The legal and policy time is the scarcer resource and
is not substitutable.

**13. What does success look like at 6 and 18 months?**
**At 6 months:** one agency has signed a pilot, one batch of tags is in the water, and reports
have been paid to members of the public who were not us. If no agency has signed by then, the
reason will be procurement or legal, and that is decision-grade information.
**At 18 months:** the reporting rate on coded tags is measurably higher than that agency's own
historical baseline for uncoded tags, measured by them and not by us. That single comparison is
the whole thesis. A second agency or the harvest workflow would be upside.

### Risk

**14. What is the most likely way this fails?**
Not technically. The most likely failure is that the pilot dies in the agency's finance office:
nobody will approve a budget line denominated in satoshis, or the state auditor will not sign
off on holding them, and it stalls for six months and quietly lapses without a tag ever being
scanned. What we would do about it: ask the treasury question in the *first* meeting rather
than the fifth, and have a fallback ready in which a third party holds the reward float and the
agency contracts for reporting outcomes rather than holding a balance itself. That fallback is
worse — it puts an intermediary back into a system designed to remove them — and we would
rather know early that it is needed than discover it late.

The second most likely failure is quieter: the pilot works, reports go up, and the agency
cannot articulate to its own leadership why a ledger was necessary rather than a database and
a payments processor. The answer is real — the record and the payment are one object, and the
dataset outlives the database — but if a biologist cannot say it in their own words after using
it, we have not made the case.

**15. What would have to be true for us to kill this after starting?**
Pre-committed, while nobody's ego is invested:

- **No signed pilot within nine months** of the first agency conversation. Not "still
  interested" — signed.
- **A pilot runs and the reporting rate does not move** against the agency's own baseline. The
  thesis is falsifiable and this is the falsification.
- **Counsel concludes the agency cannot hold satoshis** and the only workable structure puts a
  custodial intermediary between the agency and the finder. At that point the thing that made
  it worth doing is gone, and it should be archived rather than reshaped.

---

## Part 4: Appendix — measured facts

Everything cited above, measured on the running TTN deployment rather than estimated. Any
reviewer can re-run these.

| Fact | Value | How it was measured |
|---|---|---|
| Fee to write one full signed record | **98–118 satoshis** (13 activations) | wallet debits minus the 20,000 locked |
| Fee to pay a finder and re-lock the tag | **141–143 satoshis** (7 redemptions) | wallet debits minus the 5,000 paid |
| Fee for a tag's whole lifecycle | **~250 satoshis**, about 1% of the reward | activation + redemption |
| Transaction size carrying a full record | 779–1,119 bytes | raw transactions in the wallet store |
| Reward per tag | 20,000 satoshis (5,000 on report, 15,000 held for a release) | `chain.DefaultConfig` |
| Live on TTN | 22 tags minted, 13 armed, 10 reports paid, 23 transactions proven | `run-ttn` deployment |
| Chain-versus-database reconciliation | **no findings** | `wildtag audit` |
| Species supported | 2 (Atlantic blue crab, red drum); adding one is a JSON file | `internal/species/profiles/` |
| Verification | 186 Go tests, 36 app tests | `go test ./...`, `npm test` |

### What the system does and does not establish

Carried from the application's own public pages, which state the same thing to finders:

- **Established:** that a record existed by a given block and has not been altered; that
  whoever redeemed a tag held the physical tag; that the reward can be claimed exactly once.
- **Not established:** that the phone was where it says it was. The position is self-reported
  and attested, not proven, and every user-facing page says so.
- **A cost, not a feature:** redemption requires the agency's counter-signature, so a finder
  cannot take the money unilaterally. This buys replay protection across a tag's life and the
  ability to refuse a claim on a tag reported stolen. It is a genuine reduction in how
  trustless the system is, and it is documented rather than glossed.

### Sources

- SCDNR, *Don't Underestimate the Reward* — https://www.dnr.sc.gov/marine/tagfish/pdf/Sept13-Don't%20Underestimate%20the%20Reward.pdf
- SCDNR marine game fish tagging programme — https://www.dnr.sc.gov/marine/tagfish/tagfish.html
- SC Game Check, harvest reporting — https://dnr.sc.gov/harvestreport/

# Design handoffs

**Not source.** Nothing in here is built, served or embedded. These are the
deliverables the product owner sent, kept because a decision is easier to
re-read against the thing that prompted it than against a description of it.

Each folder is one handoff, named by the date it arrived and what it covered:

| Folder | What it was | Adopted as |
| --- | --- | --- |
| `2026-08-24 app redesign` | the first whole-interface mock | D42 and the lobby that followed |
| `2026-08-28 account screens` | terms, settings, sign-up, sign-in | D61 |
| `2026-08-30 lobby and room` | the lobby list and the room screen | D68 |

The reasoning for each is in `docs/decisions.md`; the stylesheet passes that
followed the third one are in `docs/2026-08-30-ui-fixes.md`.

Two later deliveries are **not** here on purpose. They contained only edited
copies of `lobbyapp/ui/`, byte-identical to what was applied from them, so the
repository already holds every line of them - see D72 and D73.

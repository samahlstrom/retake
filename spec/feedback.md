# Product feedback

An unobtrusive in-app bug and feature reporter. Fixed GitHub target samahlstrom/retake. Reports are public; only user-entered title/details and Retake build version go to GitHub. Never send page URL, project/media names, paths, tokens, email or screenshots automatically.

API: GET api/feedback returns {enabled:boolean}. POST api/feedback accepts JSON {type:"bug"|"feature",title,description}; trimmed title 1..200 characters, description 1..8000. 32 KiB body bound. Valid submissions create a GitHub issue and return 201 {url,number}. Server-only RETAKE_FEEDBACK_TOKEN enables direct submission. Missing token returns 503 with clear safe error. GitHub failure returns 502, timeout finite, no upstream body or secret leaks. Restrict fixed GitHub repository and validate returned issue URL. Rate limit submission to prevent accidental repeated issue creation. Reuse stdlib HTTP + JSON, existing auth/CSRF; viewers/reviewers/approvers/owners can report after existing auth. Unauthenticated shared visitors cannot post. Integrate single-video Server, folder home, hosted Hub. No new database or dependency.

UI: one shared Svelte component on home and review pages. Slim right-edge Feedback tab; opens small panel. Bug / Feature request radio choices, short title, details, note "Reports are public on GitHub. Do not include private information.", Send feedback. During send disable repeated sends, show Sending…; only show success after valid 201 with issue URL, then View issue and Done. Failure keeps draft and offers retry. Close/Escape preserve unsent draft. If direct submission is unavailable, clearly offer Continue on GitHub, with prefilled type/title/details, no false success; show GitHub sign-in requirement. No surprise external navigation on failure. Use native controls and existing theme variables. Hide launcher in fullscreen/phone compose, respect other modal layers; input must not trigger player shortcuts.

## Visual contract
| State | Structure/order | Position/size | Color/type | Interaction |
|---|---|---|---|---|
| closed desktop | text Feedback, no badge | right edge, middle; 32x96 px, left rounded | Retake muted surface, text, font-ui 12px | click opens; keyboard focus visible; no animation or auto-open |
| open desktop | heading+Close, type radios, title, details, public note, submit | right 16px, bottom 80px; width360px max viewport-32px, max-height viewport-96px so top stays at least16px, scroll | existing theme tokens; 13px body, 14px heading; 12px gaps,16pxpadding | accessible dialog, Escape/close restores trigger focus, draft preserved |
| mobile closed/open | same order | 32px edge tab; panel inset16px, bottom safe-area+72px, max-height viewport-88px-safe-area, scroll fits 390x844 | same tokens; inputs16px | no horizontal overflow or covered submit |
| pending | same form, Sending… | unchanged | disabled controls | no repeated submit |
| error | form plus inline alert | unchanged | danger token | draft retained; retry |
| success | Thanks. Your feedback was sent., View issue, Done | same panel | ordinary body/link tokens | issue opens new tab; Done closes |
| unavailable | form, clear GitHub sign-in text, Continue on GitHub | same panel | ordinary tokens | prefilled external issue; never says submitted |
| permissions | same for authenticated roles | same | same | absent on hosted sign-in; API refuses unauthenticated |

Closing a successful report with Done or Close resets the next form. Closing before success, including a failed send,
keeps the draft.

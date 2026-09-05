# Camoufox tool recipes

Use this reference only for the specialized workflow at hand. Exact argument
schemas from the MCP `tools/list` response are authoritative.

## Interactive session

1. Call `camoufox_session_start` with an optional `profile_name` and
   `initial_url`. Set `headless: false` only when the user needs a visible local
   browser.
2. Call `camoufox_inspect_dom` with the returned `session_id` and accessibility
   tree mode.
3. Call `camoufox_act` with the current `target_id` for click/type/select/check,
   or an action such as scroll, back, reload, switch tab, wait for URL, or bring to
   front.
4. Inspect again after page or tab changes.
5. Use `camoufox_screenshot` for visual evidence when the state matters.
6. Call `camoufox_session_save` for a persistent checkpoint or
   `camoufox_session_close` when continuation is not needed.

Use `expects_popup` for clicks known to open an OAuth/popup window. Use
`camoufox_session_list` to discover live sessions instead of guessing IDs.

## Authenticated/manual login

Start a named session in headful mode and navigate to the login page. Tell the user
to complete credentials/MFA in the visible browser. Wait for a known post-login
URL or ask the user to confirm completion, then save the session. Do not inspect
password fields, copy credentials, or export state unless the user explicitly asks
for the export.

On a server without a display, visible manual interaction may be unavailable.
Report that limitation rather than assuming a virtual display is user-accessible.

## Structured extraction

- `camoufox_fetch_page`: readable page content; use Markdown extraction by
  default.
- `camoufox_extract_json_ld`: Schema.org/product/article metadata.
- `camoufox_scrape_selector`: repeated items. Keep selectors and requested fields
  narrow.
- `camoufox_intercept_api`: dynamic JSON/GraphQL/XHR data. Use a specific
  `url_pattern` and avoid capturing unrelated authenticated traffic.
- `camoufox_discover_trends`: platform/country trend discovery when its supported
  sources match the question.

If extracted values conflict, report the source and observation time rather than
silently merging them.

## CAPTCHA or challenge

Use `camoufox_solve_captcha` only on a page the user is authorized to access and
when the challenge type is supported. Re-inspect after the attempt. A tool success
response is not proof that the site accepted the challenge; verify navigation or
page state.

Do not use automation to defeat access controls, account restrictions, purchase
limits, or another site's terms/authorization boundary.

## Screenshots and PDF

Use `camoufox_screenshot` for current visual state and `camoufox_pdf_export` for a
document-style capture. Choose explicit output paths inside the workspace when the
schema allows. Check that the artifact exists and is non-empty before linking it.

## Media

1. Call `camoufox_sniff_media` on the authorized page and inspect returned stream
   topology and metadata.
2. For a muxed stream, pass the selected video/direct URL to
   `camoufox_download_media`.
3. For separate adaptive tracks, pass the chosen video and audio URLs.
4. For a clip, provide explicit start/duration values. Accurate trimming may
   re-encode; fast trimming can begin on a keyframe.
5. Store the output under the authorized workspace, verify the file, and report
   the selected quality/container.

Do not download or redistribute media without the user's authorization and rights.
Signed stream URLs and cookies are sensitive and should not be printed in the
response or logs.

## Portable session state

`camoufox_session_export_state` and `camoufox_session_import_state` move
authentication state between authorized environments. Treat the exported file as
a credential:

- Require an explicit user request.
- Keep it inside an authorized path with restrictive permissions.
- Do not show its contents.
- Import only into the same user's/agent's named profile.
- Remove or retain it according to the user's stated workflow.

## Failure handling

- Challenge/block: capture the response/page state and report the limitation.
- Missing element: inspect again; do not keep clicking a stale target ID.
- Session missing: list sessions and resume by trusted profile name if appropriate.
- Dependency error: report the missing Camoufox/browser/FFmpeg dependency.
- Corporate/network block: report the observed block. Do not advise bypassing
  organizational policy.

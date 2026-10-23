//   import { servedIndex, catalog, playerKeyTables } from "./page_fixture.mjs";
//
// The page as the server serves it, for the node suites. web.go does not send
// web/index.html as it is: webui.go's withActionCatalog puts the action
// catalog (protocol/fixtures/actions.json, which the binary embeds) in place
// of the <!--scterm:actions--> line, as a JSON script block escaped the way
// Go's json.HTMLEscape escapes it. servedIndex() does the same with the
// fixture, so a suite that loads the page loads what the browser gets.

import { readFileSync } from "node:fs";
import vm from "node:vm";

const read = (path) => readFileSync(new URL(path, import.meta.url), "utf8");

export const ACTIONS_PLACEHOLDER = "<!--scterm:actions-->";

// catalogText is the action catalog fixture as it is on disk.
export const catalogText = () => read("../protocol/fixtures/actions.json");

// catalog is the fixture, parsed.
export const catalog = () => JSON.parse(catalogText());

// htmlEscapeJSON is Go's json.HTMLEscape: <, >, & and U+2028/U+2029 become
// \u escapes, which leave the JSON's meaning alone.
export function htmlEscapeJSON(text) {
  return text.replace(/[<>&\u2028\u2029]/g,
    (c) => "\\u" + c.charCodeAt(0).toString(16).padStart(4, "0"));
}

// withCatalog is webui.go's withActionCatalog: the placeholder, which must be
// there exactly once, becomes the catalog's script block.
export function withCatalog(html, json) {
  const at = html.indexOf(ACTIONS_PLACEHOLDER);
  if (at < 0 || html.indexOf(ACTIONS_PLACEHOLDER, at + 1) >= 0) {
    throw new Error("index.html must hold " + ACTIONS_PLACEHOLDER + " exactly once");
  }
  return html.slice(0, at) + '<script type="application/json" id="actions">' +
    htmlEscapeJSON(json) + "</script>" + html.slice(at + ACTIONS_PLACEHOLDER.length);
}

// servedIndex is index.html as the server sends it, with the fixture in it.
export const servedIndex = () => withCatalog(read("./index.html"), catalogText());

// playerKeyTables runs the input section of player.js (the part
// web/input_test.mjs lifts) against a catalog and returns the key tables its
// key handler reads, keyed by KeyboardEvent.code: Alt+<key> (alt), F1..F12
// (fkeys) and Alt+F1..F12 (altFkeys).
export function playerKeyTables(cat) {
  const src = read("./player.js");
  const start = src.indexOf("// Android keycodes");
  const end = src.indexOf("// screenshot (Alt+S)");
  if (start < 0 || end <= start) throw new Error("cannot locate the input section of player.js");
  const ctx = { CATALOG: cat };
  vm.createContext(ctx);
  vm.runInContext(src.slice(start, end), ctx);
  return JSON.parse(vm.runInContext(
    "JSON.stringify({ alt: MNEMONIC_ACTIONS, fkeys: APP_KEYS, altFkeys: ALT_FKEYS })", ctx));
}

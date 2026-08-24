// Text lookup and writing direction (D44).
//
// The owner chose English first and Persian later. What engineering does about
// that is not optional: Persian is written right-to-left, and retrofitting
// direction into a finished layout is the expensive path, because it touches
// every screen. So none of this is a translation feature - it is the shape the
// interface is built in from the start, and it costs nothing today.
//
// Three rules, enforced by lobbyapp/ui_test.go rather than by good intentions:
//
//   1. **No user-facing text is typed into the markup.** Every string has a
//      key and lives in strings/<lang>.json. `data-t="room.connect"` on an
//      element fills its text; `data-t-placeholder`, `data-t-title` and
//      `data-t-aria-label` fill those attributes.
//   2. **No hard-coded left or right**, in CSS or in JS. Logical properties
//      only: margin-inline-start, not margin-left. The layout then flips on
//      its own when `dir` changes, with no second stylesheet.
//   3. **Every key used exists, and every language has the same keys.** A
//      missing translation must be a build failure, not a screen that says
//      "room.connect" to a player.
//
// Adding Persian is then a file and a switch. That is the difference between
// "later" costing a week and costing a month.

const I18n = (() => {
  let strings = {};
  let current = 'en';

  // Languages this build carries. Persian is deliberately absent: D44 ships
  // English only. Adding it means a strings/fa.json with the same keys and one
  // more entry here - nothing else.
  const LANGUAGES = {
    en: { name: 'English', dir: 'ltr' },
    // fa: { name: 'فارسی', dir: 'rtl' },
  };

  async function load(lang) {
    if (!LANGUAGES[lang]) lang = 'en';
    const res = await fetch(`strings/${lang}.json`, { cache: 'no-cache' });
    if (!res.ok) throw new Error(`cannot load strings for ${lang}`);
    strings = await res.json();
    current = lang;

    // The document carries the language and its direction. Everything else
    // follows from these two attributes, which is exactly the point.
    document.documentElement.lang = lang;
    document.documentElement.dir = LANGUAGES[lang].dir;
    return lang;
  }

  // t looks up a key and substitutes {named} placeholders.
  //
  // A missing key returns the key itself rather than empty text. Silence would
  // hide the mistake; a visible "room.connect" on screen is ugly and gets
  // fixed, and the test catches it before anybody sees it anyway.
  function t(key, vars) {
    let s = strings[key];
    if (s === undefined) {
      console.warn('[i18n] missing key', key);
      return key;
    }
    if (vars) {
      for (const [name, value] of Object.entries(vars)) {
        s = s.split(`{${name}}`).join(String(value));
      }
    }
    return s;
  }

  // has reports whether a key exists, for code that wants to fall back rather
  // than show a key.
  function has(key) {
    return Object.prototype.hasOwnProperty.call(strings, key);
  }

  // apply fills every data-t element inside a root. Call it after any markup
  // is inserted; it is idempotent.
  function apply(root = document) {
    root.querySelectorAll('[data-t]').forEach((el) => {
      el.textContent = t(el.getAttribute('data-t'));
    });
    for (const attr of ['placeholder', 'title', 'aria-label', 'value']) {
      root.querySelectorAll(`[data-t-${attr}]`).forEach((el) => {
        el.setAttribute(attr, t(el.getAttribute(`data-t-${attr}`)));
      });
    }
  }

  // plural picks between two forms. English needs one rule; Persian needs a
  // different one, which is why the choice is here and not at each call site.
  function plural(key, n) {
    const one = `${key}.one`;
    if (n === 1 && has(one)) return t(one, { n });
    return t(`${key}.other`, { n });
  }

  return { load, t, has, apply, plural, languages: LANGUAGES, get lang() { return current; } };
})();

window.I18n = I18n;
window.t = (key, vars) => I18n.t(key, vars);

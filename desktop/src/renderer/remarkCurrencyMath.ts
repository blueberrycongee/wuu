import type { Construct, Extension, State, Tokenizer } from "micromark-util-types";
import type { Plugin } from "unified";

const isDigit = (code: number | null): boolean => code !== null && code >= 48 && code <= 57;
const prose = /[\p{Script=Han}\p{Script=Hiragana}\p{Script=Katakana}\p{Script=Hangul}]|(?:^|[\s/])[a-z]{2,}(?=[\s/]|$)/iu;
const amountWithSeparator = /^\d[\d,.]*(?:\s*[/–-])?\s*$/u;

// A dollar amount and a later dollar amount otherwise look like one inline
// formula to remark-math. Check the next delimiter without changing offsets or
// parsing code spans, so regular Markdown can still interpret the intervening
// emphasis, links and text.
const currencyPair: Construct = {
  tokenize(effects, ok, nok) {
    let content = "";
    let hasCommand = false;
    return start;

    function start(code: number | null): State | undefined {
      if (code !== 36) return nok(code);
      effects.enter("data");
      effects.consume(code);
      return first;
    }

    function first(code: number | null): State | undefined {
      if (!isDigit(code)) return nok(code);
      return between(code);
    }

    function between(code: number | null): State | undefined {
      if (code === null) return nok(code);
      if (code === 36) {
        effects.consume(code);
        return after;
      }
      content += code < 0 ? " " : String.fromCodePoint(code);
      effects.consume(code);
      return code === 92 ? afterBackslash : between;
    }

    function afterBackslash(code: number | null): State | undefined {
      if (code !== 36) hasCommand = true;
      return between(code);
    }

    function after(code: number | null): State | undefined {
      effects.exit("data");
      const nextMayStartMath = code !== null && (code === 92 || (code >= 65 && code <= 90) || (code >= 97 && code <= 122));
      const hasProse = !hasCommand && prose.test(content);
      return (isDigit(code) && (hasProse || amountWithSeparator.test(content))) || (nextMayStartMath && hasProse)
        ? ok(code)
        : nok(code);
    }
  },
};

const tokenizeCurrency: Tokenizer = function (effects, ok, nok) {
  return effects.check(currencyPair, literal, nok);

  function literal(code: number | null): State | undefined {
    if (code !== 36) return nok(code);
    effects.enter("data");
    effects.consume(code);
    effects.exit("data");
    return ok;
  }
};

const syntax: Extension = { text: { 36: { name: "currencyBeforeMath", tokenize: tokenizeCurrency } } };

const remarkCurrencyMath: Plugin = function () {
  (this.data().micromarkExtensions ??= []).push(syntax);
};

export default remarkCurrencyMath;

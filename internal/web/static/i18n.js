// A dictionary and a switcher, not a framework: every string lives as plain
// data below, the same shape as internal/species turned data into a way to
// add a whole new animal without a release -- applied here to language
// instead of species. Adding a language is adding a key to STRINGS, not
// writing new machinery; adding a page's worth of coverage is adding
// data-i18n attributes to its markup.
//
// Scoped to the redeem page for now, deliberately. It's the one page most
// likely to be somebody's very first encounter with this whole programme,
// reached by scanning a physical tag rather than choosing to visit -- unlike
// the dashboard or admin console, a finder does not get to pick this page in
// their preferred language before arriving, and South Carolina's coastal
// counties have a real Spanish-speaking population among the people most
// likely to actually find a tagged animal.
//
// What is NOT covered yet: anything built from live data (a tag's own
// status paragraph, the animal's timeline and facts, form field labels that
// come from a species profile). Translating those means the species schema
// itself carrying per-language labels, which is a real project of its own,
// not an afternoon's addition to this file.
//
// No pre-paint script here the way theme.js has one: that trick works
// because it only sets an attribute CSS can read before layout; swapping
// text content needs the elements to exist first. This file is loaded last,
// after every translatable element in the body has already been parsed, so
// apply() below runs synchronously at load time rather than waiting for
// DOMContentLoaded -- by the time a script tag this far down the page
// executes, everything above it is already in the DOM.
(function () {
  'use strict';

  const STORAGE_KEY = 'wildtag.lang';
  const DEFAULT_LANG = 'en';

  const STRINGS = {
    en: {
      tagline: 'report a tagged animal, get paid on the spot',
      noSecretTitle: 'This link is missing its tag code',
      noSecretBody:
        'The QR code on the tag carries a code that this page needs, and it only ' +
        "travels when the code is scanned directly. If you typed the address by " +
        'hand, or someone forwarded you the link, scan the tag itself with your ' +
        "phone's camera instead.",
      tagLabel: 'Tag',
      catchTitle: 'What did you catch?',
      whereLabel: 'Where you caught it',
      waitingFix: 'Waiting for a position fix…',
      useLocation: 'Use my location',
      nameLabel: 'Name this animal',
      optional: '(optional)',
      namePlaceholder: 'e.g. Old Bertha',
      nameNote:
        'Nobody has named this one yet, so it is yours to name. The name goes on ' +
        'the public record permanently and cannot be changed (by you or by anyone ' +
        'else), so pick something you would not mind a biologist reading out in ' +
        'ten years.',
      continueBtn: 'Continue',
      payingTitle: 'Getting you paid',
      stepQuote: "Work out what you're owed",
      stepAttest: 'Sign your report with your wallet',
      stepBuild: 'Build the payment',
      stepVerify: 'Check the payment really pays you',
      stepSign: 'Unlock the tag',
      stepReceive: 'Put it in your wallet',
      tryAgain: 'Try again',
      paidTitle: 'Paid',
      txLabel: 'Transaction',
      shareBtn: "Share this animal's story",
      legendTagged: 'tagged',
      legendFound: 'found',
      honestyTitle: "What this record does and doesn't prove",
      honestyTimeDt: 'The time',
      honestyTimeDd:
        'Your report is written into a Bitcoin transaction. Once a block carries ' +
        'it, the record cannot be backdated or altered: not by you, not by SCDNR, ' +
        'not by us.',
      honestyTagDt: 'That you had the tag',
      honestyTagDd:
        'The reward can only be unlocked with the key printed on the tag itself, ' +
        'so the record can only have come from someone holding it.',
      honestyOnceDt: 'That it pays once',
      honestyOnceDd:
        'The reward is a single coin. A second claim on the same tag is a ' +
        'double-spend and simply fails.',
      honestyWhereDt: 'Where you were',
      honestyWhereDd:
        'The position comes from this phone and is taken on trust. We record how ' +
        'accurate the phone claims to be, and we tie the fix to your report so it ' +
        'cannot be edited afterwards, but none of that establishes where anyone ' +
        'actually stood, and we will not pretend otherwise.',
      honestyReleaseDt: 'That you released it',
      honestyReleaseDd:
        'Also taken on trust. The bonus for putting an animal back is held rather ' +
        'than paid, and released to you only if this tag is reported again, which ' +
        'is the closest thing to evidence a release can have.',
      footerOrg: 'South Carolina Department of Natural Resources',
      footerDashboard: 'program dashboard',
      footerAbout: 'about this program',
      footerDataset: 'open dataset',
      // Not tied to any data-i18n markup -- called directly from redeem.js
      // for the copy this session's own field audit added or rewrote.
      noWalletTitle: 'No compatible wallet detected on this device.',
      noWalletBody:
        'Collecting the reward needs a BRC-100 wallet open in this browser tab -- ' +
        'the BSV Browser app on a phone, or a desktop wallet running locally. You ' +
        'can still fill this in now; it will be checked again when you submit.',
      noWalletSubmitError:
        'No BSV wallet found on this device. Open this page in BSV Browser to ' +
        'collect the reward.',
      networkRetry:
        'No signal right now. Nothing has been lost -- this will retry ' +
        'automatically the moment you have one, or tap Try again.',
    },
    es: {
      tagline: 'reporta un animal marcado y cobra al instante',
      noSecretTitle: 'A este enlace le falta el código de la etiqueta',
      noSecretBody:
        'El código QR de la etiqueta lleva un código que esta página necesita, y ' +
        'solo se transmite al escanear el código directamente. Si escribiste la ' +
        'dirección a mano, o alguien te reenvió el enlace, escanea la etiqueta con ' +
        'la cámara de tu teléfono.',
      tagLabel: 'Etiqueta',
      catchTitle: '¿Qué encontraste?',
      whereLabel: 'Dónde lo encontraste',
      waitingFix: 'Esperando la ubicación…',
      useLocation: 'Usar mi ubicación',
      nameLabel: 'Ponle un nombre a este animal',
      optional: '(opcional)',
      namePlaceholder: 'por ej., Vieja Berta',
      nameNote:
        'Todavía nadie le ha puesto nombre, así que puedes ser tú quien lo haga. ' +
        'El nombre queda en el registro público de forma permanente y no se puede ' +
        'cambiar (ni tú ni nadie más), así que elige algo que no te importe que un ' +
        'biólogo lea en voz alta dentro de diez años.',
      continueBtn: 'Continuar',
      payingTitle: 'Procesando tu pago',
      stepQuote: 'Calcular cuánto se te debe',
      stepAttest: 'Firmar tu reporte con tu billetera',
      stepBuild: 'Preparar el pago',
      stepVerify: 'Comprobar que el pago realmente te paga a ti',
      stepSign: 'Desbloquear la etiqueta',
      stepReceive: 'Recibirlo en tu billetera',
      tryAgain: 'Intentar de nuevo',
      paidTitle: 'Pagado',
      txLabel: 'Transacción',
      shareBtn: 'Compartir la historia de este animal',
      legendTagged: 'marcado',
      legendFound: 'encontrado',
      honestyTitle: 'Qué demuestra este registro y qué no',
      honestyTimeDt: 'La fecha y hora',
      honestyTimeDd:
        'Tu reporte queda escrito en una transacción de Bitcoin. Una vez que un ' +
        'bloque lo incluye, el registro no se puede adelantar, atrasar ni alterar: ' +
        'ni por ti, ni por el DNR, ni por nosotros.',
      honestyTagDt: 'Que tú tenías la etiqueta',
      honestyTagDd:
        'La recompensa solo se puede desbloquear con la clave impresa en la propia ' +
        'etiqueta, así que el registro solo pudo haber venido de quien la tenía en ' +
        'sus manos.',
      honestyOnceDt: 'Que se paga una sola vez',
      honestyOnceDd:
        'La recompensa es una sola moneda. Un segundo reclamo sobre la misma ' +
        'etiqueta es un doble gasto y simplemente falla.',
      honestyWhereDt: 'Dónde estabas',
      honestyWhereDd:
        'La ubicación viene de este teléfono y se toma de buena fe. Registramos ' +
        'qué tan precisa dice ser, y la vinculamos a tu reporte para que no se ' +
        'pueda editar después, pero nada de eso demuestra dónde estuvo realmente ' +
        'alguien parado, y no vamos a fingir lo contrario.',
      honestyReleaseDt: 'Que lo liberaste',
      honestyReleaseDd:
        'Esto también se toma de buena fe. El bono por devolver un animal se ' +
        'retiene en vez de pagarse, y se te libera solo si esta etiqueta se ' +
        'reporta de nuevo, que es lo más parecido a una prueba de liberación que ' +
        'puede existir.',
      footerOrg: 'Departamento de Recursos Naturales de Carolina del Sur',
      footerDashboard: 'panel del programa',
      footerAbout: 'acerca de este programa',
      footerDataset: 'conjunto de datos abierto',
      noWalletTitle: 'No se detectó una billetera compatible en este dispositivo.',
      noWalletBody:
        'Para cobrar la recompensa hace falta una billetera BRC-100 abierta en ' +
        'esta pestaña: la app BSV Browser en un teléfono, o una billetera de ' +
        'escritorio corriendo en este equipo. Puedes completar el formulario ' +
        'igual; se volverá a comprobar cuando lo envíes.',
      noWalletSubmitError:
        'No se encontró una billetera de BSV en este dispositivo. Abre esta ' +
        'página en BSV Browser para cobrar la recompensa.',
      networkRetry:
        'No hay señal en este momento. No se perdió nada: esto se reintentará ' +
        'automáticamente en cuanto haya señal, o toca «Intentar de nuevo».',
    },
  };

  function detectDefault() {
    try {
      const saved = localStorage.getItem(STORAGE_KEY);
      if (saved && STRINGS[saved]) return saved;
    } catch (_) {
      /* private window, or storage blocked -- fall through to the browser's
         own language guess */
    }
    const nav = (navigator.language || '').slice(0, 2).toLowerCase();
    return STRINGS[nav] ? nav : DEFAULT_LANG;
  }

  let lang = detectDefault();

  function t(key) {
    return (STRINGS[lang] && STRINGS[lang][key]) || STRINGS[DEFAULT_LANG][key] || key;
  }

  function apply(root) {
    const scope = root || document;
    scope.querySelectorAll('[data-i18n]').forEach((el) => {
      el.textContent = t(el.getAttribute('data-i18n'));
    });
    scope.querySelectorAll('[data-i18n-placeholder]').forEach((el) => {
      el.setAttribute('placeholder', t(el.getAttribute('data-i18n-placeholder')));
    });
  }

  function setLang(next) {
    if (!STRINGS[next] || next === lang) return;
    lang = next;
    try {
      localStorage.setItem(STORAGE_KEY, next);
    } catch (_) {
      /* the choice just won't survive a reload */
    }
    document.documentElement.setAttribute('lang', next);
    apply();
    document.querySelectorAll('.lang-toggle [data-lang]').forEach((btn) => {
      btn.setAttribute('aria-pressed', String(btn.getAttribute('data-lang') === next));
    });
  }

  window.I18n = {
    t,
    apply,
    setLang,
    get lang() {
      return lang;
    },
    languages: Object.keys(STRINGS),
  };

  document.documentElement.setAttribute('lang', lang);
  apply();
  document.querySelectorAll('.lang-toggle [data-lang]').forEach((btn) => {
    btn.setAttribute('aria-pressed', String(btn.getAttribute('data-lang') === lang));
    btn.addEventListener('click', () => setLang(btn.getAttribute('data-lang')));
  });
})();

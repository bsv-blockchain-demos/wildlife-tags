// A small, deterministic identicon for a signed-in session: the same wallet
// identity key (or "operator", for a password session) always draws the
// same marble, so a biologist glancing at the header can recognise their
// own session at a glance rather than reading a truncated key.
//
// Reimplemented from boringavatars.com's "marble" variant
// (https://github.com/boringdesigners/boring-avatars, MIT licensed) rather
// than pulled in as a dependency: this app has no build step and installs
// nothing, and the algorithm itself is a few dozen lines of pure math, not
// a component library. hashCode/getUnit/getRandomColor below are a direct
// port of that project's src/lib/utilities.ts, including its one quirk --
// the middle and outer shapes both scale by the *outer* shape's own scale
// value, not their own -- kept intentionally rather than "fixed", so a
// given name draws the same marble here as it would on the original site.
(function () {
  'use strict';

  function hashCode(name) {
    let hash = 0;
    for (let i = 0; i < name.length; i++) {
      hash = (hash << 5) - hash + name.charCodeAt(i);
      hash = hash & hash; // force a 32-bit integer, same as the original
    }
    return Math.abs(hash);
  }

  function getDigit(number, ntn) {
    return Math.floor((number / Math.pow(10, ntn)) % 10);
  }

  function getUnit(number, range, index) {
    const value = number % range;
    if (index && getDigit(number, index) % 2 === 0) return -value;
    return value;
  }

  function getRandomColor(number, colors, range) {
    return colors[number % range];
  }

  const ELEMENTS = 3;
  const SIZE = 80;

  function generateProperties(name, colors) {
    const numFromName = hashCode(name);
    const range = colors.length;
    return Array.from({ length: ELEMENTS }, (_, i) => ({
      color: getRandomColor(numFromName + i, colors, range),
      translateX: getUnit(numFromName * (i + 1), SIZE / 10, 1),
      translateY: getUnit(numFromName * (i + 1), SIZE / 10, 2),
      scale: 1.2 + getUnit(numFromName * (i + 1), SIZE / 20) / 10,
      rotate: getUnit(numFromName * (i + 1), 360, 1),
    }));
  }

  // A palette drawn from this app's own --grad-* tokens (see style.css)
  // rather than boringavatars.com's own defaults, so a session's avatar
  // reads as part of the same coastal palette as the species cards and
  // everything else here, instead of an unrelated set of brand colors.
  const DEFAULT_PALETTE = ['#0f5c56', '#2dd4bf', '#60a5fa', '#fbbf24', '#4ade80'];

  let uid = 0;

  // marble returns a self-contained <svg> string: a deterministic
  // three-layer blurred marble masked to a circle, seeded by `name`.
  // Purely decorative next to visible "signed in as ..." text, hence
  // aria-hidden rather than a duplicated accessible name.
  function marble(name, opts) {
    opts = opts || {};
    const colors = opts.colors && opts.colors.length ? opts.colors : DEFAULT_PALETTE;
    const size = opts.size || 32;
    const p = generateProperties(String(name || ''), colors);
    const id = `avatarMarble${uid++}`;
    // Both blurred shapes scale by p[2].scale, not their own -- see the
    // file comment above.
    const transform = (i) =>
      `translate(${p[i].translateX} ${p[i].translateY}) rotate(${p[i].rotate} ${SIZE / 2} ${SIZE / 2}) scale(${p[2].scale})`;
    return (
      `<svg viewBox="0 0 ${SIZE} ${SIZE}" width="${size}" height="${size}" fill="none" role="img" aria-hidden="true" xmlns="http://www.w3.org/2000/svg">` +
      `<mask id="${id}" maskUnits="userSpaceOnUse" x="0" y="0" width="${SIZE}" height="${SIZE}">` +
      `<rect width="${SIZE}" height="${SIZE}" rx="${SIZE * 2}" fill="#fff"/>` +
      `</mask>` +
      `<g mask="url(#${id})">` +
      `<rect width="${SIZE}" height="${SIZE}" fill="${p[0].color}"/>` +
      `<path filter="url(#filter_${id})" d="M32.414 59.35L50.376 70.5H72.5v-71H33.728L26.5 13.381l19.057 27.08L32.414 59.35z" fill="${p[1].color}" transform="${transform(1)}"/>` +
      `<path filter="url(#filter_${id})" style="mix-blend-mode:overlay" d="M22.216 24L0 46.75l14.108 38.129L78 86l-3.081-59.276-22.378 4.005 12.972 20.186-23.35 27.395L22.215 24z" fill="${p[2].color}" transform="${transform(2)}"/>` +
      `</g>` +
      `<defs><filter id="filter_${id}" filterUnits="userSpaceOnUse" color-interpolation-filters="sRGB">` +
      `<feFlood flood-opacity="0" result="BackgroundImageFix"/>` +
      `<feBlend in="SourceGraphic" in2="BackgroundImageFix" result="shape"/>` +
      `<feGaussianBlur stdDeviation="7" result="effect1_foregroundBlur"/>` +
      `</filter></defs>` +
      `</svg>`
    );
  }

  window.Avatar = { marble, DEFAULT_PALETTE };
})();

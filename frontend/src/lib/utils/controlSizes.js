/**
 * Single source of truth for interactive form-control sizing.
 *
 * Inputs, selects, comboboxes, search fields, and textareas all render these
 * classes so controls placed next to each other in a form line up instead of
 * drifting to different heights. Add or adjust sizes here rather than in the
 * individual components.
 */
export const CONTROL_SIZE_CLASSES = {
  small: 'px-3 py-1.5 text-sm',
  medium: 'px-3 py-2 text-sm',
  large: 'px-4 py-3 text-base',
};

/**
 * @param {string} [size]
 * @returns {string}
 */
export function controlSizeClasses(size = 'medium') {
  return CONTROL_SIZE_CLASSES[size] ?? CONTROL_SIZE_CLASSES.medium;
}

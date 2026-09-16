import { t } from '@/i18n'

/**
 * Stable operation-log type values shared by the database and API.
 * Display text stays in the frontend locale dictionaries.
 */
const LOG_TYPE_KEYS: Record<string, string> = {
  upload: 'LOG_TYPE_UPLOAD',
  'image.delete': 'LOG_TYPE_IMAGE_DELETE',
  'image.update': 'LOG_TYPE_IMAGE_UPDATE',
  'mail.send': 'LOG_TYPE_MAIL_SEND',
  'user.create': 'LOG_TYPE_USER_CREATE',
  'user.delete': 'LOG_TYPE_USER_DELETE',
  'user.update': 'LOG_TYPE_USER_UPDATE',
  'storage.create': 'LOG_TYPE_STORAGE_CREATE',
  'storage.update': 'LOG_TYPE_STORAGE_UPDATE',
  'storage.delete': 'LOG_TYPE_STORAGE_DELETE',
  'plugin.install': 'LOG_TYPE_PLUGIN_INSTALL',
  'plugin.uninstall': 'LOG_TYPE_PLUGIN_UNINSTALL',
  'plugin.update': 'LOG_TYPE_PLUGIN_UPDATE',
  'auth.login': 'LOG_TYPE_AUTH_LOGIN',
  'auth.failed': 'LOG_TYPE_AUTH_FAILED',
  'auth.logout': 'LOG_TYPE_AUTH_LOGOUT',
  'setting.update': 'LOG_TYPE_SETTING_UPDATE',
  'system.log.cleanup': 'LOG_TYPE_SYSTEM_LOG_CLEANUP',
  'theme.install': 'LOG_TYPE_THEME_INSTALL',
  'theme.uninstall': 'LOG_TYPE_THEME_UNINSTALL',
  'theme.activate': 'LOG_TYPE_THEME_ACTIVATE',
  'theme.rescan': 'LOG_TYPE_THEME_RESCAN',
  'theme.settings.update': 'LOG_TYPE_THEME_SETTINGS_UPDATE',
  'theme.settings.clear': 'LOG_TYPE_THEME_SETTINGS_CLEAR',
  'theme.error': 'LOG_TYPE_THEME_ERROR',
}

/** Translate a stable operation-log type without changing the value sent to the API. */
export function logTypeLabel(type: string): string {
  const key = LOG_TYPE_KEYS[type]
  return key ? t(key) : t('LOG_TYPE_UNKNOWN', { type: type || '—' })
}

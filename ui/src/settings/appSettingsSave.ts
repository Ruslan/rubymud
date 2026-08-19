import {
  saveAppSettingRequest,
  type AppSettings,
  type MutableAppSettingKey,
} from './api';

export type AppSettingSaveResult = {
  settings: AppSettings;
  saved: boolean;
  error: string;
};

/**
 * Save one app setting without letting a stale tab replace sibling fields.
 * On any HTTP, network, or response-decoding failure, return the last rendered
 * snapshot so the native control can be restored by its caller.
 */
export async function persistAppSetting<K extends MutableAppSettingKey>(
  current: AppSettings,
  key: K,
  value: AppSettings[K],
  fallbackError: string,
): Promise<AppSettingSaveResult> {
  try {
    const response = await saveAppSettingRequest(key, value);
    if (!response.ok) {
      return {
        settings: current,
        saved: false,
        error: await response.text() || fallbackError,
      };
    }
    return {
      settings: await response.json() as AppSettings,
      saved: true,
      error: '',
    };
  } catch {
    return { settings: current, saved: false, error: fallbackError };
  }
}

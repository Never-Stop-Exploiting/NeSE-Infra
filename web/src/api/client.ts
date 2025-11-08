import axios, { AxiosRequestConfig } from "axios";

export const api = axios.create({
  baseURL: "",
  timeout: 10000
});

type TokenProvider = () => string | undefined;

let authTokenProvider: TokenProvider | null = null;

export function setAuthTokenProvider(provider: TokenProvider) {
  authTokenProvider = provider;
}

api.interceptors.request.use((config: AxiosRequestConfig) => {
  const token = authTokenProvider?.();
  if (token) {
    config.headers = config.headers ?? {};
    config.headers.Authorization = `Bearer ${token}`;
  }
  return config;
});

api.interceptors.response.use(
  (response) => response,
  (error) => {
    if (error.response) {
      console.error("API error", error.response.status, error.response.data);
    } else {
      console.error("Network error", error);
    }
    return Promise.reject(error);
  }
);

export function fallback<T>(promise: Promise<T>, fallbackValue: T): Promise<T> {
  return promise.catch(() => fallbackValue);
}

import axios from "axios";

export const api = axios.create({
  baseURL: "",
  timeout: 10000
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

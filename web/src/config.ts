export interface WarehouseRuntimeConfig {
  apiOrigin?: string;
}

declare global {
  interface Window {
    __WAREHOUSE_CONFIG__?: WarehouseRuntimeConfig;
  }
}

const ORDER_API_PATH = "/api/order-management";
const DEV_ORDER_API_BASE = "http://localhost:8086";

export function resolveOrderApiBase(
  runtimeConfig: WarehouseRuntimeConfig,
  isProduction: boolean,
): string {
  const apiOrigin = runtimeConfig.apiOrigin?.replace(/\/+$/, "");
  if (!apiOrigin) {
    if (isProduction) {
      throw new Error("window.__WAREHOUSE_CONFIG__.apiOrigin is required in production");
    }
    return DEV_ORDER_API_BASE;
  }
  return `${apiOrigin}${ORDER_API_PATH}`;
}

export const ORDER_API_BASE = resolveOrderApiBase(
  window.__WAREHOUSE_CONFIG__ ?? {},
  import.meta.env.PROD,
);

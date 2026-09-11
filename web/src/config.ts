/** Local-dev base URL for order-management's own REST API. Mirrors
 *  e2e-tests/env.sh's port-offset convention (each service's own :8080
 *  default, offset by index -- FACILITY=8081, INVENTORY=8082, WES=8083,
 *  FULFILLMENT=8084, WORKFORCE=8085, ORDER=8086, PROCESS_PATH=8087,
 *  LABOR=8088). Verified directly against e2e-tests/env.sh's
 *  ORDER_HTTP_PORT=8086. */
export const ORDER_API_BASE = "http://localhost:8086";

import { OrderManagementScreen } from "./screens/OrderManagementScreen";

/** Exposed as order_mgmt_mfe/App via Module Federation. Lazy-loaded by
 *  warehouse-console's App.tsx as `import("order_mgmt_mfe/App")`.
 *
 *  order-management's REST API has exactly three endpoints this remote's
 *  v1 scope covers (place an order, look up an order by id, cancel an
 *  order) -- there is no list/search endpoint, so this stays one screen
 *  with two panels rather than a list+detail shape, mirroring
 *  labor-performance's own single-screen precedent for a similarly-thin
 *  REST surface. */
export default function App() {
  return <OrderManagementScreen />;
}

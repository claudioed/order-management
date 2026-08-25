import type { SidebarsConfig } from "@docusaurus/plugin-content-docs";

const sidebar: SidebarsConfig = {
  apisidebar: [
    {
      type: "doc",
      id: "api-reference/rest/order-management-api",
    },
    {
      type: "category",
      label: "Orders",
      link: {
        type: "doc",
        id: "api-reference/rest/orders",
      },
      items: [
        {
          type: "doc",
          id: "api-reference/rest/receive-order",
          label: "Receive a new order",
          className: "api-method post",
        },
        {
          type: "doc",
          id: "api-reference/rest/get-order",
          label: "Read an order's current state",
          className: "api-method get",
        },
        {
          type: "doc",
          id: "api-reference/rest/cancel-order",
          label: "Cancel an order and revoke its reservations",
          className: "api-method delete",
        },
      ],
    },
    {
      type: "category",
      label: "Allocation",
      link: {
        type: "doc",
        id: "api-reference/rest/allocation",
      },
      items: [
        {
          type: "doc",
          id: "api-reference/rest/allocate-order",
          label: "Allocate stock for every pending line",
          className: "api-method post",
        },
        {
          type: "doc",
          id: "api-reference/rest/retry-allocation",
          label: "Re-attempt allocation for backordered lines only",
          className: "api-method post",
        },
      ],
    },
    {
      type: "category",
      label: "Release",
      link: {
        type: "doc",
        id: "api-reference/rest/release",
      },
      items: [
        {
          type: "doc",
          id: "api-reference/rest/release-order",
          label: "Release allocated lines as work",
          className: "api-method post",
        },
      ],
    },
    {
      type: "category",
      label: "Health",
      link: {
        type: "doc",
        id: "api-reference/rest/health",
      },
      items: [
        {
          type: "doc",
          id: "api-reference/rest/get-healthz",
          label: "Liveness probe",
          className: "api-method get",
        },
      ],
    },
  ],
};

export default sidebar.apisidebar;

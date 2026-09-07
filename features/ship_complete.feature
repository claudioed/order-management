Feature: Ship-complete default (BR3)
  With AllowPartialShipment=false (the default), a single backordered line
  blocks the WHOLE order: nothing releases, order status is Backordered,
  and only RetryAllocation can unblock it.

  Scenario: One backordered line blocks the whole order
    Given the Order Management service is running
    And inventory-storage has usable stock for SKU "SKU-BOOK-0001"
    And inventory-storage reports insufficient usable stock for SKU "SKU-TOY-9999"
    When an order is placed for SKUs "SKU-BOOK-0001,SKU-TOY-9999"
    Then the request is accepted with status 201
    And the order status is "Backordered"
    And line 1 status is "Allocated"
    And line 2 status is "Backordered"

  Scenario: Restocked retry releases the whole order
    Given the Order Management service is running
    And inventory-storage has usable stock for SKU "SKU-BOOK-0001"
    And inventory-storage reports insufficient usable stock for SKU "SKU-TOY-9999"
    And an order is placed for SKUs "SKU-BOOK-0001,SKU-TOY-9999"
    And inventory-storage has usable stock for SKU "SKU-TOY-9999"
    When the order is retried for allocation
    Then the request is accepted with status 200
    And the order status is "Released"
    And line 1 status is "Released"
    And line 2 status is "Released"

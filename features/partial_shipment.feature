Feature: Partial shipment
  With AllowPartialShipment=true, allocated lines are independently
  eligible for release even while other lines are backordered.

  Scenario: Eligible lines release despite a backordered sibling
    Given the Order Management service is running
    And inventory-storage has usable stock for SKU "SKU-BOOK-0001"
    And inventory-storage reports insufficient usable stock for SKU "SKU-TOY-9999"
    When an order allowing partial shipment is placed for SKUs "SKU-BOOK-0001,SKU-TOY-9999"
    Then the request is accepted with status 201
    And the order status is "PartiallyReleased"
    And line 1 status is "Released"
    And line 2 status is "Backordered"

  Scenario: Restocked retry completes the order
    Given the Order Management service is running
    And inventory-storage has usable stock for SKU "SKU-BOOK-0001"
    And inventory-storage reports insufficient usable stock for SKU "SKU-TOY-9999"
    And an order allowing partial shipment is placed for SKUs "SKU-BOOK-0001,SKU-TOY-9999"
    And inventory-storage has usable stock for SKU "SKU-TOY-9999"
    When the order is retried for allocation
    Then the request is accepted with status 200
    And the order status is "Released"

Feature: Cancellation boundary (BR6)
  CancelOrder is legal ONLY while no line has reached Released. A legal
  cancellation revokes every allocated line's reservation upstream. Once
  any line is Released the request is rejected — v1 does not claw back
  released work (documented known gap).

  Scenario: A legal cancellation revokes allocated reservations
    Given the Order Management service is running
    And inventory-storage has usable stock for SKU "SKU-BOOK-0001"
    And inventory-storage reports insufficient usable stock for SKU "SKU-TOY-9999"
    And an order is placed for SKUs "SKU-BOOK-0001,SKU-TOY-9999"
    When the order is cancelled
    Then the request is accepted with status 204
    And the reservation for SKU "SKU-BOOK-0001" was revoked

  Scenario: A released order can no longer be cancelled
    Given the Order Management service is running
    And inventory-storage has usable stock for SKU "SKU-BOOK-0001"
    And an order is placed for SKUs "SKU-BOOK-0001"
    When the order is cancelled
    Then the request is rejected with status 409
    And the problem detail title is "Order already has released lines and can no longer be cancelled"

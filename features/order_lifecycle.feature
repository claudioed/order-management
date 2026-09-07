Feature: Order lifecycle — choreographed release
  A caller expresses ONE intent, placing an order. Allocation and release
  are internal saga steps (ADR 0005): an order whose lines all reserve
  stock is carried through to Released in the same POST /orders call.

  Scenario: A fully stocked order is released in the same call
    Given the Order Management service is running
    And inventory-storage has usable stock for SKU "SKU-BOOK-0001"
    When an order is placed for SKUs "SKU-BOOK-0001"
    Then the request is accepted with status 201
    And the order status is "Released"
    And line 1 status is "Released"
    And the order has a promise date

  Scenario: A multi-line order releases every line
    Given the Order Management service is running
    And inventory-storage has usable stock for SKU "SKU-BOOK-0001"
    And inventory-storage has usable stock for SKU "SKU-TOY-0042"
    When an order is placed for SKUs "SKU-BOOK-0001,SKU-TOY-0042"
    Then the request is accepted with status 201
    And the order status is "Released"
    And line 1 status is "Released"
    And line 2 status is "Released"

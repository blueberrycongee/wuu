import XCTest

final class AccountUITests: XCTestCase {
    func testAccountServerAgentRoundTrip() throws {
        let environment = ProcessInfo.processInfo.environment
        guard let server = environment["WUU_TEST_SERVER"], let username = environment["WUU_TEST_USERNAME"], let password = environment["WUU_TEST_PASSWORD"] else {
            throw XCTSkip("Provide TEST_RUNNER_WUU_TEST_SERVER, TEST_RUNNER_WUU_TEST_USERNAME and TEST_RUNNER_WUU_TEST_PASSWORD for the live Agent test")
        }
        let app = XCUIApplication(bundleIdentifier: "com.blueberrycongee.wuu")
        app.launch()
        let logout = app.buttons["退出账号"]
        if logout.waitForExistence(timeout: 5) {
            for _ in 0..<30 { if logout.isHittable { break }; app.swipeUp() }
            logout.tap()
        }
        let serverField = app.textFields.element(boundBy: 0)
        XCTAssertTrue(serverField.waitForExistence(timeout: 20))
        serverField.tap(); serverField.typeText(server)
        let usernameField = app.textFields.element(boundBy: 1)
        usernameField.tap(); usernameField.typeText(username)
        let passwordField = app.secureTextFields.firstMatch
        passwordField.tap(); passwordField.typeText(password)
        app.buttons["登录"].tap()
        let open = app.buttons["打开"].firstMatch
        XCTAssertTrue(open.waitForExistence(timeout: 30)); open.tap()
        let composer = app.textViews.firstMatch
        XCTAssertTrue(composer.waitForExistence(timeout: 30)); composer.tap()
        composer.typeText("Create ios-container-proof.txt containing IOS_CONTAINER_VERIFIED in this workspace. Reply only IOS_CONTAINER_VERIFIED.")
        let send = app.buttons.matching(NSPredicate(format: "label IN %@", ["发送", "Send"])).firstMatch
        XCTAssertTrue(send.waitForExistence(timeout: 10)); send.tap()
        let answer = app.staticTexts["IOS_CONTAINER_VERIFIED"].firstMatch
        XCTAssertTrue(answer.waitForExistence(timeout: 120))
        XCUIDevice.shared.press(.home); app.activate()
        XCTAssertTrue(answer.waitForExistence(timeout: 30))
        app.terminate(); app.launch()
        XCTAssertTrue(answer.waitForExistence(timeout: 30))
        let attachment = XCTAttachment(screenshot: app.screenshot())
        attachment.name = "container-agent-restored"; attachment.lifetime = .keepAlways; add(attachment)
        app.buttons["‹ 电脑"].tap()
    }

    func testDeviceListCanReachAccountActions() throws {
        let app = XCUIApplication(bundleIdentifier: "com.blueberrycongee.wuu")
        app.launch()
        let logout = app.buttons["退出账号"]
        XCTAssertTrue(logout.waitForExistence(timeout: 30), "Sign in to a disposable account and return to its device list before this test")
        for _ in 0..<30 {
            if logout.isHittable { break }
            app.swipeUp()
        }
        XCTAssertTrue(logout.isHittable, "Long device lists must keep account actions reachable")
        let attachment = XCTAttachment(screenshot: app.screenshot())
        attachment.name = "account-actions-reachable"
        attachment.lifetime = .keepAlways
        add(attachment)
    }
}

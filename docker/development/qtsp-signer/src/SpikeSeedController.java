/*
 [SPIKE] Development-only helper (active under the "noauth" Spring profile) that mints a
 credential directly via CredentialsService.createECDSAP256Credential — exercising the live
 SoftHSM key-gen + wrap + self-sign path with EJBCA disabled — and returns its id, so the
 signHash endpoint can be driven without a real authorize/create OAuth flow.
 NOT part of the CSC API. Never enable the noauth profile outside local spikes.
 */

package eu.europa.ec.eudi.signer.r3.resource_server.web.controllers;

import eu.europa.ec.eudi.signer.r3.resource_server.model.CredentialsService;
import java.util.HashMap;
import java.util.Map;
import org.slf4j.Logger;
import org.slf4j.LoggerFactory;
import org.springframework.beans.factory.annotation.Autowired;
import org.springframework.context.annotation.Profile;
import org.springframework.web.bind.annotation.GetMapping;
import org.springframework.web.bind.annotation.PostMapping;
import org.springframework.web.bind.annotation.RequestMapping;
import org.springframework.web.bind.annotation.RequestParam;
import org.springframework.web.bind.annotation.RestController;

@RestController
@Profile("noauth")
@RequestMapping(value = "/spike")
public class SpikeSeedController {

    private static final Logger logger = LoggerFactory.getLogger(SpikeSeedController.class);
    private final CredentialsService credentialsService;

    public SpikeSeedController(@Autowired CredentialsService credentialsService) {
        this.credentialsService = credentialsService;
    }

    // Unauthenticated liveness probe for the compose healthcheck: 200 once the
    // Spring context (and therefore the CSC endpoints) is up.
    @GetMapping(value = "/health", produces = "application/json")
    public Map<String, String> health() {
        return Map.of("status", "ok");
    }

    @PostMapping(value = "/seed-credential", produces = "application/json")
    public Map<String, String> seedCredential(@RequestParam(defaultValue = "noauth-dev-user") String sub) throws Exception {
        logger.warn("[SPIKE] Seeding an ECDSA P-256 credential for sub={} (SoftHSM key-gen + wrap + self-sign, no EJBCA).", sub);
        String credentialId = this.credentialsService.createECDSAP256Credential(sub, "Dev", "User", "Dev User", "UT");
        Map<String, String> body = new HashMap<>();
        body.put("credentialID", credentialId);
        body.put("userID", sub);
        logger.warn("[SPIKE] Seeded credential id={} for sub={}", credentialId, sub);
        return body;
    }
}

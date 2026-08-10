package com.rd.robot.security;

import org.mindrot.jbcrypt.BCrypt;

/**
 * Password hashing and validation using bcrypt.
 * Replaces MD5 hashing for security.
 */
public class PasswordEncoder {

    /** bcrypt cost factor, 12 is the current recommended value (~300ms/hash) */
    private static final int BCRYPT_COST = 12;

    /** Minimum password length */
    private static final int MIN_PASSWORD_LEN = 6;

    private PasswordEncoder() {}

    /**
     * Hash a password using bcrypt.
     */
    public static String hashPassword(String password) {
        return BCrypt.hashpw(password, BCrypt.gensalt(BCRYPT_COST));
    }

    /**
     * Verify a password against a bcrypt hash.
     */
    public static boolean verifyPassword(String password, String hash) {
        try {
            return BCrypt.checkpw(password, hash);
        } catch (Exception e) {
            return false;
        }
    }

    /**
     * Validate password complexity: at least 6 characters.
     */
    public static void validatePassword(String password) {
        if (password == null || password.codePointCount(0, password.length()) < MIN_PASSWORD_LEN) {
            throw new IllegalArgumentException("密码长度至少 " + MIN_PASSWORD_LEN + " 个字符");
        }
    }
}